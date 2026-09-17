// Package publicapi — «что видит внешний мир»: опубликованные через gateway ruto маршруты
// сервиса и трафик по ним из метрик gateway (ТЗ 5.2). Внешняя картина дополняет внутренние
// метрики сервиса: расхождение между ними — сам по себе диагностический сигнал.
package publicapi

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	"github.com/mechta-market/pulse/internal/constant"
	dependencyModel "github.com/mechta-market/pulse/internal/domain/dependency/model"
	"github.com/mechta-market/pulse/internal/errs"
	prometheusModel "github.com/mechta-market/pulse/internal/service/prometheus/model"
	rutoModel "github.com/mechta-market/pulse/internal/service/ruto/model"
	"github.com/mechta-market/pulse/internal/usecase/publicapi/model"
	"github.com/mechta-market/pulse/internal/util/window"
)

// errorStatusRe — ошибка сервера: HTTP 5xx или серверный код gRPC (как в мониторинге ruto)
var errorStatusRe = regexp.MustCompile(`^(5\d\d|Internal|Unknown|Unavailable|DeadlineExceeded|ResourceExhausted|DataLoss|Unimplemented)$`)

// Config — метрики gateway и лимиты (из yaml-правил).
type Config struct {
	Deadline     time.Duration
	MaxEndpoints int
	// RequestsMetric / DurationMetric — метрики gateway с лейблами app, method, status
	RequestsMetric string
	DurationMetric string
}

type Usecase struct {
	conf Config

	svc        svcServiceI
	depend     dependencyServiceI
	ruto       RutoI
	prometheus PrometheusI
}

func New(conf Config, svc svcServiceI, depend dependencyServiceI, ruto RutoI, prometheus PrometheusI) *Usecase {
	if conf.Deadline <= 0 {
		conf.Deadline = 8 * time.Second
	}
	if conf.MaxEndpoints <= 0 {
		conf.MaxEndpoints = 100
	}
	return &Usecase{conf: conf, svc: svc, depend: depend, ruto: ruto, prometheus: prometheus}
}

func (u *Usecase) PublicApi(ctx context.Context, serviceName string, win time.Duration) (*model.PublicApi, error) {
	if u.ruto == nil {
		return nil, fmt.Errorf("%w: ruto is not configured (RUTO_URL)", errs.ServiceNA)
	}
	if win <= 0 {
		win = window.Default
	}

	service, err := u.svc.GetOrSuggest(ctx, serviceName)
	if err != nil {
		return nil, fmt.Errorf("svc.GetOrSuggest: %w", err)
	}

	edges, _, err := u.depend.List(ctx, &dependencyModel.ListReq{ToServices: []string{service.Name}})
	if err != nil {
		return nil, fmt.Errorf("depend.List: %w", err)
	}
	appNames := lo.Uniq(lo.FilterMap(edges, func(e *dependencyModel.Main, _ int) (string, bool) {
		return e.Key, e.Source == dependencyModel.SourceRuto
	}))
	if len(appNames) == 0 {
		return nil, errs.ErrFull{Err: errs.ObjectNotFound, Desc: fmt.Sprintf(
			"%s is not published through ruto gateway (no gateway app routes to it); it is internal-only or the indexer has not seen ruto yet", service.Name)}
	}

	ctx, cancel := context.WithTimeout(ctx, u.conf.Deadline)
	defer cancel()

	result := &model.PublicApi{Service: service.Name, Window: win}
	c := &collector{u: u, result: result, appNames: appNames, win: win, now: time.Now().UTC()}

	eg, egCtx := errgroup.WithContext(ctx)
	eg.Go(func() error { c.config(egCtx); return nil })
	eg.Go(func() error { c.traffic(egCtx); return nil })
	_ = eg.Wait()

	c.finish()
	return result, nil
}

// collector — состояние одного сбора: конфигурация ruto и метрики gateway параллельно.
type collector struct {
	u        *Usecase
	result   *model.PublicApi
	appNames []string
	win      time.Duration
	now      time.Time

	mu sync.Mutex
	// configured — маршрут из конфигурации ruto → приложение и тип
	configured map[string]model.Route
	// requests / errors / p95 — по маршруту из метрик
	requests map[string]float64
	failures map[string]float64
	p95      map[string]float64
	statuses map[string]float64
	totalP95 *float64
}

func (c *collector) addError(source string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.result.Errors = append(c.result.Errors, model.SourceError{Source: source, Message: err.Error()})
}

func (c *collector) config(ctx context.Context) {
	snapshot, err := c.u.ruto.GetSnapshot(ctx)
	if err != nil {
		c.addError(constant.SourceRuto, err)
		return
	}

	configured := make(map[string]model.Route, 64)
	apps := make([]model.App, 0, len(c.appNames))
	for _, app := range snapshot.Apps {
		if !lo.Contains(c.appNames, app.Name) {
			continue
		}
		active := lo.Filter(app.Endpoints, func(e rutoModel.Endpoint, _ int) bool { return e.Active })
		apps = append(apps, model.App{
			Name: app.Name, PathPrefix: app.PathPrefix, BackendUrl: app.BackendUrl, GrpcUrl: app.GrpcUrl,
			Endpoints: len(active), InactiveEndpoints: len(app.Endpoints) - len(active),
		})
		if !app.Active {
			continue
		}
		for _, e := range active {
			route := app.Route(e)
			configured[route] = model.Route{App: app.Name, Route: route, Type: e.Type, Configured: true}
		}
	}

	c.mu.Lock()
	c.result.BaseUrl = snapshot.BaseUrl
	c.result.Apps = apps
	c.configured = configured
	c.mu.Unlock()
}

// traffic — три мгновенных запроса за окно: запросы по маршруту и коду, p95 по маршруту и общий.
func (c *collector) traffic(ctx context.Context) {
	if c.u.prometheus == nil {
		c.addError(constant.SourcePrometheus, errs.Err("not configured"))
		return
	}

	apps := `{app=~"` + promRegexAlternation(c.appNames) + `"}`
	rangeWin := "[" + promDuration(c.win) + "]"
	queries := map[string]string{
		"requests":  `sum by (app, method, status) (increase(` + c.u.conf.RequestsMetric + apps + rangeWin + `))`,
		"route_p95": `histogram_quantile(0.95, sum by (method, le) (rate(` + c.u.conf.DurationMetric + `_bucket` + apps + rangeWin + `)))`,
		"total_p95": `histogram_quantile(0.95, sum by (le) (rate(` + c.u.conf.DurationMetric + `_bucket` + apps + rangeWin + `)))`,
	}

	samples := make(map[string][]prometheusModel.Sample, len(queries))
	eg, egCtx := errgroup.WithContext(ctx)
	for key, promql := range queries {
		eg.Go(func() error {
			res, err := c.u.prometheus.Query(egCtx, promql, c.now)
			if err != nil {
				c.addError(constant.SourcePrometheus, err)
				return nil
			}
			c.mu.Lock()
			samples[key] = res
			c.mu.Unlock()
			return nil
		})
	}
	_ = eg.Wait()

	requests, failures, statuses := map[string]float64{}, map[string]float64{}, map[string]float64{}
	for _, s := range samples["requests"] {
		if !finite(s.Value) {
			continue
		}
		route, status := s.Labels["method"], s.Labels["status"]
		requests[route] += s.Value
		statuses[status] += s.Value
		if errorStatusRe.MatchString(status) {
			failures[route] += s.Value
		}
	}
	p95 := lo.SliceToMap(lo.Filter(samples["route_p95"], func(s prometheusModel.Sample, _ int) bool { return finite(s.Value) }),
		func(s prometheusModel.Sample) (string, float64) { return s.Labels["method"], s.Value })

	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests, c.failures, c.statuses, c.p95 = requests, failures, statuses, p95
	if total := samples["total_p95"]; len(total) > 0 && finite(total[0].Value) {
		c.totalP95 = new(total[0].Value)
	}
}

// finish сводит конфигурацию и трафик: маршруты по убыванию запросов, сводка, усечение.
func (c *collector) finish() {
	seconds := c.win.Seconds()
	routes := make(map[string]model.Route, len(c.configured)+len(c.requests))
	for key, r := range c.configured {
		routes[key] = r
	}
	for key, count := range c.requests {
		r, ok := routes[key]
		if !ok {
			r = model.Route{Route: key, Type: routeType(key)}
		}
		r.Requests, r.Rps, r.Errors = round(count), round(count/seconds), round(c.failures[key])
		if count > 0 {
			r.ErrorRate = new(round(c.failures[key] / count))
		}
		if v, ok := c.p95[key]; ok {
			r.P95 = new(round(v))
		}
		routes[key] = r
	}

	list := lo.Values(routes)
	sort.Slice(list, func(i, j int) bool {
		if list[i].Requests != list[j].Requests {
			return list[i].Requests > list[j].Requests
		}
		return list[i].Route < list[j].Route
	})
	c.result.TotalCount = len(list)
	if len(list) > c.u.conf.MaxEndpoints {
		list, c.result.Truncated = list[:c.u.conf.MaxEndpoints], true
	}
	c.result.Routes = list

	if c.requests != nil {
		total := lo.Sum(lo.Values(c.requests))
		traffic := &model.Traffic{Requests: round(total), Rps: round(total / seconds), P95: c.totalP95}
		if total > 0 {
			traffic.ErrorRate = new(round(lo.Sum(lo.Values(c.failures)) / total))
		}
		for status, count := range c.statuses {
			share := 0.0
			if total > 0 {
				share = round(count / total)
			}
			traffic.Statuses = append(traffic.Statuses, model.StatusShare{Status: status, Requests: round(count), Share: share})
		}
		sort.Slice(traffic.Statuses, func(i, j int) bool { return traffic.Statuses[i].Requests > traffic.Statuses[j].Requests })
		c.result.Traffic = traffic
	}

	c.result.Errors = lo.UniqBy(c.result.Errors, func(e model.SourceError) string { return e.Source })
}

func routeType(route string) string {
	if strings.HasPrefix(route, "GRPC ") {
		return rutoModel.EndpointTypeGrpc
	}
	return rutoModel.EndpointTypeHttp
}

// promDuration — окно в синтаксисе PromQL (целые секунды).
func promDuration(d time.Duration) string {
	return fmt.Sprintf("%ds", int64(d.Seconds()))
}

// promRegexAlternation — a|b для label-матчера PromQL с экранированием метасимволов; обратный
// слеш удваивается, потому что строка PromQL в двойных кавычках сама разбирает escape-последовательности.
func promRegexAlternation(values []string) string {
	return strings.Join(lo.Map(values, func(v string, _ int) string {
		return strings.ReplaceAll(regexp.QuoteMeta(v), `\`, `\\`)
	}), "|")
}

func finite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// round — три значащих знака после запятой: increase() даёт дробные счётчики, модели они не нужны.
func round(v float64) float64 {
	return math.Round(v*1000) / 1000
}
