package timeline

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	"github.com/mechta-market/pulse/internal/constant"
	deployModel "github.com/mechta-market/pulse/internal/domain/deploy/model"
	eventModel "github.com/mechta-market/pulse/internal/domain/event/model"
	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	"github.com/mechta-market/pulse/internal/errs"
	githubModel "github.com/mechta-market/pulse/internal/service/github/model"
	"github.com/mechta-market/pulse/internal/usecase/timeline/model"
)

// alertsQuery — история срабатываний: Alertmanager не хранит историю, её даёт Prometheus
const (
	alertsQuery    = `ALERTS{alertstate="firing"}`
	alertsMaxStep  = 5 * time.Minute
	alertsMinStep  = time.Minute
	alertsPoints   = 300
	podsConcurrent = 5
)

// collector — состояние одного сбора; источники пишут под мьютексом.
type collector struct {
	u         *Usecase
	services  []*svcModel.Main
	workloads []*workloadModel.Main
	since     time.Time
	now       time.Time

	mu     sync.Mutex
	events []eventModel.Event
	errors []model.SourceError
}

func (c *collector) add(events ...eventModel.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, events...)
}

func (c *collector) addError(source string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errors = append(c.errors, model.SourceError{Source: source, Message: compactError(err)})
}

func (c *collector) names() []string {
	return lo.Map(c.services, func(s *svcModel.Main, _ int) string { return s.Name })
}

// serviceOf — сервис по имени объекта кластера: имя workload'а или его префикс.
func (c *collector) serviceOf(namespace, objectName string) (string, bool) {
	for _, w := range c.workloads {
		if w.Namespace == namespace && (objectName == w.Name || strings.HasPrefix(objectName, w.Name+"-")) {
			return w.ServiceName, true
		}
	}
	return "", false
}

func (c *collector) deploys(ctx context.Context) {
	deploys, _, err := c.u.deploy.List(ctx, &deployModel.ListReq{ServiceNames: c.names(), Since: &c.since})
	if err != nil {
		c.addError(constant.SourcePostgres, err)
		return
	}
	c.add(lo.Map(deploys, func(d *deployModel.Main, _ int) eventModel.Event { return c.u.events.FromDeploy(d) })...)
}

// clusterEvents — события кластера за окно плюс рестарты/OOM из статусов подов.
func (c *collector) clusterEvents(ctx context.Context, clusterScope bool) {
	namespaces := []string{""}
	if !clusterScope {
		namespaces = lo.Uniq(lo.Map(c.workloads, func(w *workloadModel.Main, _ int) string { return w.Namespace }))
	}

	for _, ns := range namespaces {
		events, err := c.u.k8s.ListEvents(ctx, ns, c.since)
		if err != nil {
			c.addError(constant.SourceK8s, fmt.Errorf("events %q: %w", ns, err))
			continue
		}
		collected := make([]eventModel.Event, 0, len(events))
		for _, e := range events {
			service, ok := c.serviceOf(e.Namespace, e.ObjectName)
			if !ok {
				continue
			}
			if converted, ok := c.u.events.FromCluster(eventModel.ClusterEvent{
				TS: e.LastTS, ObjectKind: e.ObjectKind, ObjectName: e.ObjectName, Reason: e.Reason, Type: e.Type, Message: e.Message, Count: e.Count,
			}, service); ok {
				collected = append(collected, converted)
			}
		}
		c.add(collected...)
	}

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(podsConcurrent)
	for _, w := range c.workloads {
		if w.Selector == "" {
			continue
		}
		eg.Go(func() error {
			pods, err := c.u.k8s.ListPods(egCtx, w.Namespace, w.Selector)
			if err != nil {
				c.addError(constant.SourceK8s, fmt.Errorf("pods %s/%s: %w", w.Namespace, w.Name, err))
				return nil
			}
			for _, pod := range pods {
				for _, ct := range pod.Containers {
					if ct.LastTerminatedAt.Before(c.since) {
						continue
					}
					if event, ok := c.u.events.FromTermination(eventModel.ContainerTermination{
						At: ct.LastTerminatedAt, Pod: pod.Name, Container: ct.Name, Reason: ct.LastTerminationReason, Restarts: ct.Restarts,
					}, w.ServiceName); ok {
						c.add(event)
					}
				}
			}
			return nil
		})
	}
	_ = eg.Wait()
}

// configuration — выкатки reloader'а, правки и sync kusec (withKusec — только в разрезе сервисов).
func (c *collector) configuration(ctx context.Context, withKusec bool) {
	c.add(c.u.configuration(ctx, c.workloads, c.since, c.now, withKusec, false, c.addError).events(c.u.events)...)
}

// alertHistory — интервалы firing из метрики ALERTS: начало серии = срабатывание.
// Алерт, горевший уже к началу окна, — не изменение: в разрезе кластера он пропускается
// (иначе давно горящие инфра-алерты забивают ленту), для сервиса — помечается как «горит с
// начала окна». Служебные алерты (Watchdog, severity=none) не показываются.
func (c *collector) alertHistory(ctx context.Context, clusterScope bool) {
	if c.u.prometheus == nil {
		c.addError(constant.SourcePrometheus, errs.Err("not configured"))
		return
	}

	step := min(max(c.now.Sub(c.since)/alertsPoints, alertsMinStep), alertsMaxStep)
	series, err := c.u.prometheus.QueryRange(ctx, alertsQuery, c.since, c.now, step)
	if err != nil {
		c.addError(constant.SourcePrometheus, err)
		return
	}

	names := lo.Uniq(append(lo.Map(c.workloads, func(w *workloadModel.Main, _ int) string { return w.Name }), c.names()...))
	byWorkload := lo.SliceToMap(c.workloads, func(w *workloadModel.Main) (string, string) { return w.Name, w.ServiceName })

	for _, s := range series {
		if len(s.Points) == 0 || !c.u.rules.AlertMatches(s.Labels, names) || isMonitoringAlert(s.Labels) {
			continue
		}
		alreadyFiring := !s.Points[0].TS.After(c.since.Add(step))
		if alreadyFiring && clusterScope {
			continue
		}
		service := c.serviceForLabels(s.Labels, byWorkload)
		alertName := s.Labels["alertname"]
		severity := s.Labels["severity"]

		// серия может рваться (алерт погас и сработал снова): каждый разрыв больше 2 шагов — новое срабатывание
		start := s.Points[0].TS
		last := start
		labels := compactAlertLabels(s.Labels)
		flush := func(end time.Time) {
			summary := fmt.Sprintf("%s: сработал алерт %s (%s)", service, alertName, lo.CoalesceOrEmpty(severity, "severity не задан"))
			if alreadyFiring && start.Equal(s.Points[0].TS) {
				summary = fmt.Sprintf("%s: алерт %s (%s) горит с начала окна или раньше", service, alertName, lo.CoalesceOrEmpty(severity, "severity не задан"))
			}
			if end.Before(c.now.Add(-2 * step)) {
				summary += fmt.Sprintf(", погас через %s", end.Sub(start).Round(time.Minute))
			}
			c.add(eventModel.Event{
				TS: start, Source: constant.SourcePrometheus, Type: constant.EventTypeAlertFiring, Service: service,
				Severity: c.u.rules.AlertSeverity(severity), Summary: summary,
				Details: map[string]any{"labels": labels, "ended_at": end},
			})
		}
		for _, p := range s.Points[1:] {
			if p.TS.Sub(last) > 2*step {
				flush(last)
				start = p.TS
			}
			last = p.TS
		}
		flush(last)
	}
}

// служебные лейблы Prometheus/экспортеров, бесполезные модели и раздувающие ответ
var noisyAlertLabels = []string{"prometheus", "endpoint", "instance", "job", "metrics_path", "uid", "container_id", "image_id"}

func compactAlertLabels(labels map[string]string) map[string]string {
	return lo.OmitByKeys(labels, noisyAlertLabels)
}

// isMonitoringAlert — служебный алерт проверки самого мониторинга, а не проблема.
func isMonitoringAlert(labels map[string]string) bool {
	return labels["alertname"] == "Watchdog" || labels["alertname"] == "InfoInhibitor" || labels["severity"] == "none"
}

func (c *collector) serviceForLabels(labels map[string]string, byWorkload map[string]string) string {
	for _, key := range []string{"service", "app", "deployment", "statefulset", "daemonset", "workload", "job", "container"} {
		if service, ok := byWorkload[labels[key]]; ok {
			return service
		}
		if lo.Contains(c.names(), labels[key]) {
			return labels[key]
		}
	}
	return lo.CoalesceOrEmpty(c.names()...)
}

func (c *collector) commits(ctx context.Context) {
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(3)
	for _, service := range c.services {
		if service.RepoUrl == "" {
			continue
		}
		eg.Go(func() error {
			commits, err := c.u.github.ListCommits(egCtx, service.RepoUrl, c.since, c.now, c.u.conf.CommitsLimit)
			if err != nil {
				c.addError(constant.SourceGithub, err)
				return nil
			}
			c.add(lo.Map(commits, func(cm githubModel.Commit, _ int) eventModel.Event {
				return eventModel.Event{
					TS: cm.Date, Source: constant.SourceGithub, Type: constant.EventTypeCommit, Service: service.Name,
					Severity: constant.SeverityInfo,
					Summary:  fmt.Sprintf("%s: коммит %s, автор %s: %s", service.Name, cm.ShortSHA(), cm.Author, cm.Message),
					Details:  map[string]any{"sha": cm.SHA, "author": cm.Author, "url": cm.Url},
				}
			})...)
			return nil
		})
	}
	_ = eg.Wait()
}

func encodeCommit(v githubModel.Commit, _ int) model.Commit {
	return model.Commit{SHA: v.SHA, Author: v.Author, Message: v.Message, Date: v.Date, Url: v.Url}
}
