// Package snapshot — главный инструмент сервиса: параллельный сбор из всех источников
// с общим дедлайном и частичным результатом (Р1), состояние всегда живьём (Р4),
// метрики в сравнении с базовой линией (Р6).
package snapshot

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	"github.com/mechta-market/pulse/internal/constant"
	dependencyModel "github.com/mechta-market/pulse/internal/domain/dependency/model"
	eventModel "github.com/mechta-market/pulse/internal/domain/event/model"
	snapshotModel "github.com/mechta-market/pulse/internal/domain/snapshot/model"
	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	"github.com/mechta-market/pulse/internal/errs"
	prometheusModel "github.com/mechta-market/pulse/internal/service/prometheus/model"
	"github.com/mechta-market/pulse/internal/util/window"
)

// Config — параметры сбора (из yaml-правил).
type Config struct {
	Deadline  time.Duration
	MaxEvents int
	MaxAlerts int
	// DefaultMetrics — golden signals, когда в service.yaml нет metrics
	DefaultMetrics []snapshotModel.MetricDef
	// PublicMetrics — метрики gateway ruto с плейсхолдером {ruto_apps}; добавляются, когда у
	// сервиса есть приложения ruto
	PublicMetrics []snapshotModel.MetricDef

	// ограничения query_metrics
	MaxWindow time.Duration
	MaxSeries int
	MaxPoints int

	// TopErrors — сколько error-паттернов логов класть в снапшот
	TopErrors int
}

type Usecase struct {
	conf Config

	svc          svcServiceI
	workload     workloadServiceI
	depend       dependencyServiceI
	k8s          k8sClientI
	prometheus   PrometheusI
	alertmanager AlertmanagerI
	logs         LogsI
	events       eventServiceI
	rules        rulesServiceI
}

func New(
	conf Config,
	svc svcServiceI,
	workload workloadServiceI,
	depend dependencyServiceI,
	k8s k8sClientI,
	prometheus PrometheusI,
	alertmanager AlertmanagerI,
	logs LogsI,
	events eventServiceI,
	rules rulesServiceI,
) *Usecase {
	if conf.Deadline <= 0 {
		conf.Deadline = 5 * time.Second
	}
	if conf.TopErrors <= 0 {
		conf.TopErrors = 3
	}
	if conf.MaxWindow <= 0 {
		conf.MaxWindow = window.Max
	}
	return &Usecase{
		conf:         conf,
		svc:          svc,
		workload:     workload,
		depend:       depend,
		k8s:          k8s,
		prometheus:   prometheus,
		alertmanager: alertmanager,
		logs:         logs,
		events:       events,
		rules:        rules,
	}
}

// Snapshot собирает срез состояния сервиса. Ошибка любого источника не роняет ответ:
// частичный результат плюс errors.
func (u *Usecase) Snapshot(ctx context.Context, serviceName string, win time.Duration) (*snapshotModel.Snapshot, error) {
	if win <= 0 {
		win = window.Default
	}

	service, err := u.svc.GetOrSuggest(ctx, serviceName)
	if err != nil {
		return nil, fmt.Errorf("svc.GetOrSuggest: %w", err)
	}

	workloads, _, err := u.workload.List(ctx, &workloadModel.ListReq{ServiceName: new(service.Name)})
	if err != nil {
		return nil, fmt.Errorf("workload.List: %w", err)
	}

	now := time.Now().UTC()
	snap := &snapshotModel.Snapshot{
		Service:     service.Name,
		GeneratedAt: now,
		Window:      win,
		Workloads: lo.Map(workloads, func(w *workloadModel.Main, _ int) snapshotModel.WorkloadState {
			return snapshotModel.WorkloadState{
				Namespace: w.Namespace, Kind: w.Kind, Name: w.Name,
				ReplicasDesired: w.ReplicasDesired, Image: w.Image, DeployedCommit: w.DeployedCommit,
			}
		}),
	}

	collector := &collector{u: u, snap: snap, service: service, workloads: workloads, now: now}

	ctx, cancel := context.WithTimeout(ctx, u.conf.Deadline)
	defer cancel()

	// fan-out: каждый источник пишет свою часть и свою ошибку; общий дедлайн — ctx
	eg, egCtx := errgroup.WithContext(ctx)
	eg.Go(func() error { collector.pods(egCtx); return nil })
	eg.Go(func() error { collector.events(egCtx); return nil })
	eg.Go(func() error { collector.alerts(egCtx); return nil })
	eg.Go(func() error { collector.metrics(egCtx); return nil })
	eg.Go(func() error { collector.topErrors(egCtx); return nil })
	_ = eg.Wait()

	collector.finish()

	return snap, nil
}

// collector — состояние одного сбора; каждый метод-источник трогает только свои поля
// снапшота и добавляет ошибки под мьютексом.
type collector struct {
	u         *Usecase
	snap      *snapshotModel.Snapshot
	service   *svcModel.Main
	workloads []*workloadModel.Main
	now       time.Time

	mu              sync.Mutex
	podsUnavailable bool
	podEvents       []eventModel.Event
}

func (c *collector) addError(source string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snap.Errors = append(c.snap.Errors, snapshotModel.SourceError{Source: source, Message: compactError(err)})
}

var urlRe = regexp.MustCompile(`https?://[^\s"]+`)

// compactError убирает URL из текста ошибки: модели важна причина, а не адрес с query-строкой,
// который повторяется в каждой из десятков ошибок и раздувает ответ.
func compactError(err error) string {
	return urlRe.ReplaceAllString(err.Error(), "<url>")
}

// pods — живое состояние подов каждого workload'а; из статусов контейнеров выводятся
// события рестартов и OOM за окно.
func (c *collector) pods(ctx context.Context) {
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(5)

	var failed int
	var failedMu sync.Mutex

	for i, w := range c.workloads {
		if w.Selector == "" {
			continue
		}
		eg.Go(func() error {
			pods, err := c.u.k8s.ListPods(egCtx, w.Namespace, w.Selector)
			if err != nil {
				failedMu.Lock()
				failed++
				failedMu.Unlock()
				c.addError(constant.SourceK8s, fmt.Errorf("pods %s/%s: %w", w.Namespace, w.Name, err))
				return nil
			}

			state, events := podsState(c.u.events, pods, c.service.Name, c.now, c.snap.Window)
			c.mu.Lock()
			c.snap.Workloads[i].Pods = state
			c.podEvents = append(c.podEvents, events...)
			c.mu.Unlock()
			return nil
		})
	}
	_ = eg.Wait()

	c.mu.Lock()
	c.podsUnavailable = failed > 0 && failed == lo.CountBy(c.workloads, func(w *workloadModel.Main) bool { return w.Selector != "" })
	c.mu.Unlock()
}

// events — события кластера за окно по объектам сервиса (поды, ReplicaSet, сам workload).
func (c *collector) events(ctx context.Context) {
	namespaces := lo.Uniq(lo.Map(c.workloads, func(w *workloadModel.Main, _ int) string { return w.Namespace }))
	since := c.now.Add(-c.snap.Window)

	var collected []eventModel.Event
	for _, ns := range namespaces {
		events, err := c.u.k8s.ListEvents(ctx, ns, since)
		if err != nil {
			c.addError(constant.SourceK8s, fmt.Errorf("events %s: %w", ns, err))
			continue
		}
		for _, e := range events {
			if e.Namespace != ns || !c.belongs(e.ObjectName) {
				continue
			}
			if converted, ok := c.u.events.FromCluster(eventModel.ClusterEvent{
				TS: e.LastTS, ObjectKind: e.ObjectKind, ObjectName: e.ObjectName, Reason: e.Reason, Type: e.Type, Message: e.Message, Count: e.Count,
			}, c.service.Name); ok {
				collected = append(collected, converted)
			}
		}
	}

	c.mu.Lock()
	c.snap.RecentEvents = append(c.snap.RecentEvents, collected...)
	c.mu.Unlock()
}

// belongs — объект принадлежит сервису: имя workload'а или его префикс (поды, ReplicaSet, Job).
func (c *collector) belongs(objectName string) bool {
	for _, w := range c.workloads {
		if objectName == w.Name || strings.HasPrefix(objectName, w.Name+"-") {
			return true
		}
	}
	return false
}

// alerts — активные алерты Alertmanager, относящиеся к сервису по значениям лейблов.
func (c *collector) alerts(ctx context.Context) {
	if c.u.alertmanager == nil {
		return
	}

	alerts, err := c.u.alertmanager.ListAlerts(ctx)
	if err != nil {
		c.addError(constant.SourceAlertmanager, err)
		return
	}

	names := lo.Uniq(append(
		lo.Map(c.workloads, func(w *workloadModel.Main, _ int) string { return w.Name }),
		c.service.Name,
	))

	result := make([]snapshotModel.Alert, 0)
	var events []eventModel.Event
	for _, a := range alerts {
		if !c.u.rules.AlertMatches(a.Labels, names) {
			continue
		}
		alert := snapshotModel.Alert{
			Name:        a.Name(),
			Severity:    a.Severity(),
			State:       a.State,
			StartsAt:    a.StartsAt,
			Summary:     lo.CoalesceOrEmpty(a.Annotations["summary"], a.Annotations["description"], a.Annotations["message"]),
			Labels:      c.u.rules.AlertLabels(a.Labels),
			Annotations: a.Annotations,
		}
		result = append(result, alert)

		if a.State == "active" && !a.StartsAt.Before(c.now.Add(-c.snap.Window)) {
			events = append(events, eventModel.Event{
				TS:       a.StartsAt,
				Source:   constant.SourceAlertmanager,
				Type:     constant.EventTypeAlertFiring,
				Service:  c.service.Name,
				Severity: c.u.rules.AlertSeverity(alert.Severity),
				Summary:  fmt.Sprintf("%s: сработал алерт %s (%s)", c.service.Name, alert.Name, lo.CoalesceOrEmpty(alert.Summary, alert.Severity)),
				Details:  map[string]any{"labels": alert.Labels},
			})
		}
	}

	sort.SliceStable(result, func(i, j int) bool { return result[i].StartsAt.After(result[j].StartsAt) })
	if c.u.conf.MaxAlerts > 0 && len(result) > c.u.conf.MaxAlerts {
		result = result[:c.u.conf.MaxAlerts]
	}

	c.mu.Lock()
	c.snap.Alerts = result
	c.snap.RecentEvents = append(c.snap.RecentEvents, events...)
	c.mu.Unlock()
}

// metrics — три точки на метрику (сейчас / час назад / вчера) параллельно.
func (c *collector) metrics(ctx context.Context) {
	if c.u.prometheus == nil {
		c.addError(constant.SourcePrometheus, errs.Err("not configured"))
		return
	}

	defs := c.u.metricDefs(c.service, c.workloads, c.u.rutoApps(ctx, c.service.Name))
	metrics := make([]snapshotModel.Metric, len(defs))

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(8)

	var firstErr error
	for i, def := range defs {
		metrics[i].MetricDef = def
		targets := []struct {
			at   time.Time
			dest **float64
		}{
			{c.now, &metrics[i].Current},
			{c.now.Add(-time.Hour), &metrics[i].HourAgo},
			{c.now.Add(-24 * time.Hour), &metrics[i].SameTimeYesterday},
		}
		for _, target := range targets {
			eg.Go(func() error {
				samples, err := c.u.prometheus.Query(egCtx, def.PromQL, target.at)
				c.mu.Lock()
				defer c.mu.Unlock()
				if err != nil {
					metrics[i].Error = compactError(err)
					if firstErr == nil {
						firstErr = err
					}
					return nil
				}
				if len(samples) > 0 {
					*target.dest = new(lo.SumBy(samples, func(s prometheusModel.Sample) float64 { return s.Value }))
				}
				return nil
			})
		}
	}
	_ = eg.Wait()

	// хотя бы один запрос не прошёл — источник частично недоступен, модель должна это знать
	if firstErr != nil {
		c.addError(constant.SourcePrometheus, firstErr)
	}

	for i := range metrics {
		c.u.rules.ApplyBaseline(&metrics[i])
	}

	c.mu.Lock()
	c.snap.Metrics = metrics
	c.mu.Unlock()
}

// topErrors — верхние error-паттерны логов за окно (фаза 3): снапшот говорит не «деградировал»,
// а «падает на connection refused к acquirer-gateway».
func (c *collector) topErrors(ctx context.Context) {
	if c.u.logs == nil {
		c.addError(constant.SourceLoki, errs.Err("not configured"))
		return
	}

	patterns, err := c.u.logs.TopErrors(ctx, c.service, c.workloads, c.snap.Window, c.u.conf.TopErrors)
	if err != nil {
		c.addError(constant.SourceLoki, err)
		return
	}

	c.mu.Lock()
	c.snap.TopErrors = patterns
	c.mu.Unlock()
}

// finish — детерминированные выводы после сбора: события, health, подсказки.
func (c *collector) finish() {
	c.snap.RecentEvents = append(c.snap.RecentEvents, c.podEvents...)
	sort.SliceStable(c.snap.RecentEvents, func(i, j int) bool {
		return c.snap.RecentEvents[i].TS.After(c.snap.RecentEvents[j].TS)
	})
	if c.u.conf.MaxEvents > 0 && len(c.snap.RecentEvents) > c.u.conf.MaxEvents {
		c.snap.RecentEvents = c.snap.RecentEvents[:c.u.conf.MaxEvents]
	}

	// один источник в errors один раз: модели важен факт, а не число неудачных запросов
	c.snap.Errors = lo.UniqBy(c.snap.Errors, func(e snapshotModel.SourceError) string { return e.Source })

	c.snap.Health = c.u.rules.ComputeHealth(c.snap, c.podsUnavailable)
	c.snap.SummaryHints = c.u.rules.SummaryHints(c.snap, c.now)
}

// metricDefs — метрики из service.yaml, иначе дефолтные golden signals с подстановкой
// {namespace}, {pod_regex}, {service}; плюс внешние метрики gateway, если сервис опубликован в ruto.
func (u *Usecase) metricDefs(service *svcModel.Main, workloads []*workloadModel.Main, rutoApps []string) []snapshotModel.MetricDef {
	var defs []snapshotModel.MetricDef
	switch {
	case len(service.Metadata.Metrics) > 0:
		defs = lo.Map(service.Metadata.Metrics, func(m svcModel.Metric, _ int) snapshotModel.MetricDef {
			return snapshotModel.MetricDef{Id: m.Id, Title: m.Title, PromQL: m.PromQL, Unit: m.Unit, Direction: m.Direction}
		})
	case len(workloads) > 0:
		names := lo.Uniq(lo.Map(workloads, func(w *workloadModel.Main, _ int) string { return w.Name }))
		replacer := strings.NewReplacer(
			"{namespace}", workloads[0].Namespace,
			"{pod_regex}", "^("+strings.Join(names, "|")+")-.*",
			"{service}", service.Name,
		)
		defs = lo.Map(u.conf.DefaultMetrics, func(m snapshotModel.MetricDef, _ int) snapshotModel.MetricDef {
			m.PromQL = replacer.Replace(m.PromQL)
			return m
		})
	}

	if len(rutoApps) == 0 {
		return defs
	}
	apps := strings.NewReplacer("{ruto_apps}", promRegexAlternation(rutoApps))
	return append(defs, lo.Map(u.conf.PublicMetrics, func(m snapshotModel.MetricDef, _ int) snapshotModel.MetricDef {
		m.PromQL = apps.Replace(m.PromQL)
		return m
	})...)
}

// rutoApps — имена приложений gateway ruto, ведущих на сервис (рёбра индексера). Ошибка
// чтения не мешает снапшоту: внешних метрик просто не будет.
func (u *Usecase) rutoApps(ctx context.Context, service string) []string {
	if len(u.conf.PublicMetrics) == 0 {
		return nil
	}
	edges, _, err := u.depend.List(ctx, &dependencyModel.ListReq{ToServices: []string{service}})
	if err != nil {
		slog.Warn("snapshot: ruto apps are unavailable", "service", service, "error", err)
		return nil
	}
	return lo.Uniq(lo.FilterMap(edges, func(e *dependencyModel.Main, _ int) (string, bool) {
		return e.Key, e.Source == dependencyModel.SourceRuto
	}))
}

// promRegexAlternation — (a|b) для label-матчера PromQL: метасимволы экранируются, а обратный
// слеш удваивается, потому что строка PromQL в двойных кавычках сама разбирает escape-последовательности.
func promRegexAlternation(values []string) string {
	quoted := lo.Map(values, func(v string, _ int) string {
		return strings.ReplaceAll(regexp.QuoteMeta(v), `\`, `\\`)
	})
	return strings.Join(quoted, "|")
}
