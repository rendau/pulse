package cluster

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	"github.com/rendau/pulse/internal/constant"
	clusterModel "github.com/rendau/pulse/internal/domain/cluster/model"
	dependencyModel "github.com/rendau/pulse/internal/domain/dependency/model"
	prometheusModel "github.com/rendau/pulse/internal/service/prometheus/model"
	rutoModel "github.com/rendau/pulse/internal/service/ruto/model"
)

// PublicConfig — gateway ruto: сервис каталога и метрики с лейблами app, status.
type PublicConfig struct {
	GatewayService string
	RequestsMetric string
	DurationMetric string
	// MaxApps — сколько проблемных приложений в ответе; LogLines — сколько строк ошибок gateway разбирать
	MaxApps  int
	LogLines int
}

// errorStatusRe — ошибка сервера: HTTP 5xx или серверный код gRPC (как в мониторинге ruto)
var errorStatusRe = regexp.MustCompile(`^(5\d\d|Internal|Unknown|Unavailable|DeadlineExceeded|ResourceExhausted|DataLoss|Unimplemented)$`)

// exampleLen — сколько символов текста ошибки в примере
const exampleLen = 200

// public — сырьё для проблем публичных приложений: трафик из метрик gateway, ошибки из его
// логов, рёбра «gateway → backend» из каталога. Сводится в publicApps после подов.
type public struct {
	traffic map[string]*clusterModel.PublicTraffic
	// backend — ошибки backend'а по приложению и причине; script — ошибки скриптов по приложению
	backend map[string]map[string]*clusterModel.GatewayReason
	script  map[string]map[string]*clusterModel.ScriptError
	// routes — приложение → сервис-бэкенд каталога
	routes map[string]string
}

func (c *collector) publicEnabled() bool {
	return c.u.ruto != nil && c.u.conf.Public.GatewayService != ""
}

// publicTraffic — трафик приложений за окно и обычный уровень: доля сбоев за сутки до окна,
// запросы окном раньше и вчера, p95 сейчас, вчера и окном раньше (p95 за сутки — тяжёлый запрос).
func (c *collector) publicTraffic(ctx context.Context) {
	if c.u.prometheus == nil {
		return // «not configured» уже в errors от metrics
	}
	conf := c.u.conf.Public
	win := promDuration(c.h.Window)
	requests, buckets := conf.RequestsMetric, conf.DurationMetric+"_bucket"
	p95 := func(offset string) string {
		return `histogram_quantile(0.95, sum by (app, le) (rate(` + buckets + `[` + win + `]` + offset + `)))`
	}
	queries := map[string]string{
		"now":           `sum by (app, status) (increase(` + requests + `[` + win + `]))`,
		"usual":         `sum by (app, status) (increase(` + requests + `[1d] offset ` + win + `))`,
		"prev":          `sum by (app) (increase(` + requests + `[` + win + `] offset ` + win + `))`,
		"yesterday":     `sum by (app) (increase(` + requests + `[` + win + `] offset 1d))`,
		"p95":           p95(""),
		"p95_prev":      p95(" offset " + win),
		"p95_yesterday": p95(" offset 1d"),
	}

	samples := make(map[string][]prometheusModel.Sample, len(queries))
	eg, egCtx := errgroup.WithContext(ctx)
	for key, promql := range queries {
		eg.Go(func() error {
			res, err := c.u.prometheus.Query(egCtx, promql, c.now)
			if err != nil {
				c.addError(constant.SourcePrometheus, fmt.Errorf("gateway %s: %w", key, err))
				return nil
			}
			c.mu.Lock()
			samples[key] = res
			c.mu.Unlock()
			return nil
		})
	}
	_ = eg.Wait()
	if samples["now"] == nil {
		return
	}

	traffic := map[string]*clusterModel.PublicTraffic{}
	get := func(app string) *clusterModel.PublicTraffic {
		if traffic[app] == nil {
			traffic[app] = &clusterModel.PublicTraffic{}
		}
		return traffic[app]
	}
	for _, s := range samples["now"] {
		t := get(s.Labels["app"])
		t.Requests += s.Value
		if errorStatusRe.MatchString(s.Labels["status"]) {
			t.Errors += s.Value
		}
	}
	usual := map[string][2]float64{} // запросы, сбои
	for _, s := range samples["usual"] {
		v := usual[s.Labels["app"]]
		v[0] += s.Value
		if errorStatusRe.MatchString(s.Labels["status"]) {
			v[1] += s.Value
		}
		usual[s.Labels["app"]] = v
	}
	byApp := func(key string) map[string]float64 {
		return lo.SliceToMap(samples[key], func(s prometheusModel.Sample) (string, float64) { return s.Labels["app"], s.Value })
	}
	prev, yesterday := byApp("prev"), byApp("yesterday")
	p95Now, p95Prev, p95Yesterday := byApp("p95"), byApp("p95_prev"), byApp("p95_yesterday")

	// приложения, у которых обычно есть трафик, — даже если сейчас их нет в метриках
	for app := range lo.Assign(prev, yesterday) {
		get(app)
	}
	for app, t := range traffic {
		if t.Requests > 0 {
			t.ErrorRate = new(t.Errors / t.Requests)
		}
		if v := usual[app]; v[0] > 0 {
			t.UsualErrorRate = new(v[1] / v[0])
		}
		if v, ok := prev[app]; ok {
			t.PrevRequests = new(v)
		}
		if v, ok := yesterday[app]; ok {
			t.YesterdayRequests = new(v)
		}
		if v, ok := p95Now[app]; ok {
			t.P95 = new(v)
		}
		if v, ok := lo.Coalesce(p95Yesterday[app], p95Prev[app]); ok {
			t.UsualP95 = new(v)
		}
	}
	delete(traffic, "")

	c.mu.Lock()
	c.public.traffic = traffic
	c.mu.Unlock()
}

// gatewayErrors — ошибки из логов gateway за окно: backend не ответил (по причинам) и сломанные
// скрипты трансформации (маршрут — из конфигурации gateway).
func (c *collector) gatewayErrors(ctx context.Context) {
	if c.u.logs == nil {
		return
	}
	lines, err := c.u.logs.ServiceLines(ctx, c.u.conf.Public.GatewayService, c.u.ruto.GatewayErrorsFilter(), c.h.Window, c.u.conf.Public.LogLines)
	if err != nil {
		c.addError(constant.SourceLoki, fmt.Errorf("gateway logs: %w", err))
		return
	}

	backend := map[string]map[string]*clusterModel.GatewayReason{}
	script := map[string]map[string]*clusterModel.ScriptError{}
	var scriptErrors []*rutoModel.GatewayError
	for _, line := range lines {
		e, ok := c.u.ruto.ParseGatewayError(line.Text)
		if !ok {
			continue
		}
		if e.Kind == rutoModel.GatewayErrorScript {
			scriptErrors = append(scriptErrors, e)
			continue
		}
		if backend[e.AppName] == nil {
			backend[e.AppName] = map[string]*clusterModel.GatewayReason{}
		}
		r := backend[e.AppName][e.Reason]
		if r == nil {
			r = &clusterModel.GatewayReason{Reason: e.Reason, Example: lo.Ellipsis(e.Error, exampleLen)}
			backend[e.AppName][e.Reason] = r
		}
		r.Count++
	}

	if len(scriptErrors) > 0 {
		// приложение и маршрут скрипта — по id из конфигурации gateway
		snapshot, err := c.u.ruto.GetSnapshot(ctx)
		if err != nil {
			c.addError(constant.SourceRuto, err)
		}
		apps := map[string]rutoModel.App{}
		if snapshot != nil {
			apps = lo.SliceToMap(snapshot.Apps, func(a rutoModel.App) (string, rutoModel.App) { return a.Id, a })
		}
		for _, e := range scriptErrors {
			app, route := apps[e.AppId], ""
			name := lo.CoalesceOrEmpty(app.Name, "app "+e.AppId)
			if ep, ok := lo.Find(app.Endpoints, func(ep rutoModel.Endpoint) bool { return ep.Id == e.EndpointId }); ok {
				route = app.Route(ep)
			}
			if script[name] == nil {
				script[name] = map[string]*clusterModel.ScriptError{}
			}
			key := e.EndpointId + "|" + e.Reason
			s := script[name][key]
			if s == nil {
				s = &clusterModel.ScriptError{Route: route, Reason: e.Reason, Example: lo.Ellipsis(e.Error, exampleLen)}
				script[name][key] = s
			}
			s.Count++
		}
	}

	c.mu.Lock()
	c.public.backend, c.public.script = backend, script
	c.mu.Unlock()
}

// publicRoutes — приложение gateway → сервис-бэкенд по рёбрам индексера.
func (c *collector) publicRoutes(ctx context.Context) {
	edges, _, err := c.u.depend.List(ctx, &dependencyModel.ListReq{FromServices: []string{c.u.conf.Public.GatewayService}})
	if err != nil {
		c.addError("catalog", fmt.Errorf("depend.List: %w", err))
		return
	}
	routes := map[string]string{}
	for _, e := range edges {
		if e.Source == dependencyModel.SourceRuto && (routes[e.Key] == "" || e.ToService != "") {
			routes[e.Key] = e.ToService
		}
	}

	c.mu.Lock()
	c.public.routes = routes
	c.mu.Unlock()
}

// publicApps — сводка по приложениям и правила; вызывается после сбора (нужны поды).
func (c *collector) publicApps(readyPods map[string]int) {
	p := &c.public
	names := lo.Uniq(lo.Flatten([][]string{lo.Keys(p.traffic), lo.Keys(p.backend), lo.Keys(p.script), lo.Keys(p.routes)}))

	desired := map[string]int{}
	for _, w := range c.workloads {
		if w.Kind == constant.WorkloadKindDeployment || w.Kind == constant.WorkloadKindStatefulSet {
			desired[w.ServiceName] += int(w.ReplicasDesired)
		}
	}

	apps := lo.Map(names, func(name string, _ int) *clusterModel.PublicApp {
		app := &clusterModel.PublicApp{App: name, Service: p.routes[name], Traffic: p.traffic[name]}
		app.BackendErrors = lo.Map(lo.Values(p.backend[name]), func(r *clusterModel.GatewayReason, _ int) clusterModel.GatewayReason { return *r })
		sort.Slice(app.BackendErrors, func(i, j int) bool { return app.BackendErrors[i].Count > app.BackendErrors[j].Count })
		app.ScriptErrors = lo.Map(lo.Values(p.script[name]), func(s *clusterModel.ScriptError, _ int) clusterModel.ScriptError { return *s })
		sort.Slice(app.ScriptErrors, func(i, j int) bool { return app.ScriptErrors[i].Count > app.ScriptErrors[j].Count })
		if readyPods != nil && app.Service != "" && desired[app.Service] > 0 {
			app.BackendPods = &clusterModel.PodsReady{Ready: readyPods[app.Service], Desired: desired[app.Service]}
		}
		return app
	})

	apps = c.u.rules.PublicProblems(apps, c.h.Window)
	sort.Slice(apps, func(i, j int) bool {
		if len(apps[i].Problems) != len(apps[j].Problems) {
			return len(apps[i].Problems) > len(apps[j].Problems)
		}
		return apps[i].App < apps[j].App
	})
	if len(apps) > c.u.conf.Public.MaxApps {
		apps = apps[:c.u.conf.Public.MaxApps]
	}
	c.h.PublicApps = lo.Map(apps, func(a *clusterModel.PublicApp, _ int) clusterModel.PublicApp { return *a })
}

// promDuration — окно в синтаксисе PromQL (целые секунды).
func promDuration(d time.Duration) string {
	return fmt.Sprintf("%ds", int64(d.Seconds()))
}
