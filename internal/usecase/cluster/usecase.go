// Package cluster — «у нас всё лежит или только платежи»: здоровье кластера в целом.
package cluster

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	"github.com/mechta-market/pulse/internal/constant"
	clusterModel "github.com/mechta-market/pulse/internal/domain/cluster/model"
	snapshotModel "github.com/mechta-market/pulse/internal/domain/snapshot/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	"github.com/mechta-market/pulse/internal/errs"
	alertmanagerModel "github.com/mechta-market/pulse/internal/service/alertmanager/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
	prometheusModel "github.com/mechta-market/pulse/internal/service/prometheus/model"
	"github.com/mechta-market/pulse/internal/util/imageref"
	"github.com/mechta-market/pulse/internal/util/window"
)

// Config — лимиты (из yaml-правил).
type Config struct {
	Deadline        time.Duration
	MaxProblemPods  int
	MaxEventReasons int
	MaxInfraAlerts  int
	Metrics         []snapshotModel.MetricDef
}

type Usecase struct {
	conf Config

	workload     workloadServiceI
	k8s          k8sClientI
	prometheus   PrometheusI
	alertmanager AlertmanagerI
	rules        rulesServiceI
	baseline     baselineServiceI
}

func New(conf Config, workload workloadServiceI, k8s k8sClientI, prometheus PrometheusI, alertmanager AlertmanagerI, rules rulesServiceI, baseline baselineServiceI) *Usecase {
	if conf.Deadline <= 0 {
		conf.Deadline = 8 * time.Second
	}
	if conf.MaxProblemPods <= 0 {
		conf.MaxProblemPods = 50
	}
	if conf.MaxEventReasons <= 0 {
		conf.MaxEventReasons = 15
	}
	if conf.MaxInfraAlerts <= 0 {
		conf.MaxInfraAlerts = 30
	}
	return &Usecase{conf: conf, workload: workload, k8s: k8s, prometheus: prometheus, alertmanager: alertmanager, rules: rules, baseline: baseline}
}

const pendingGrace = 2 * time.Minute

func (u *Usecase) Health(ctx context.Context, win time.Duration) (*clusterModel.Health, error) {
	if win <= 0 {
		win = window.Default
	}

	// топология нужна, чтобы привязать проблемные поды и алерты к сервисам каталога
	workloads, _, err := u.workload.List(ctx, &workloadModel.ListReq{})
	if err != nil {
		return nil, fmt.Errorf("workload.List: %w", err)
	}

	now := time.Now().UTC()
	c := &collector{
		u:         u,
		h:         &clusterModel.Health{GeneratedAt: now, Window: win},
		workloads: workloads,
		services:  lo.SliceToMap(workloads, func(w *workloadModel.Main) (string, struct{}) { return w.ServiceName, struct{}{} }),
		now:       now,
	}

	ctx, cancel := context.WithTimeout(ctx, u.conf.Deadline)
	defer cancel()

	eg, egCtx := errgroup.WithContext(ctx)
	eg.Go(func() error { c.nodes(egCtx); return nil })
	eg.Go(func() error { c.pods(egCtx); return nil })
	eg.Go(func() error { c.events(egCtx); return nil })
	eg.Go(func() error { c.alerts(egCtx); return nil })
	eg.Go(func() error { c.metrics(egCtx); return nil })
	_ = eg.Wait()

	c.h.Errors = lo.UniqBy(c.h.Errors, func(e snapshotModel.SourceError) string { return e.Source })
	c.h.Health = u.rules.ComputeHealth(c.h, c.nodesUnavailable)
	c.h.SummaryHints = u.rules.SummaryHints(c.h, now)

	return c.h, nil
}

type collector struct {
	u         *Usecase
	h         *clusterModel.Health
	workloads []*workloadModel.Main
	services  map[string]struct{} // имена сервисов каталога
	now       time.Time

	mu               sync.Mutex
	nodesUnavailable bool
}

func (c *collector) addError(source string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.h.Errors = append(c.h.Errors, snapshotModel.SourceError{Source: source, Message: compactError(err)})
}

func (c *collector) nodes(ctx context.Context) {
	nodes, err := c.u.k8s.ListNodes(ctx)
	if err != nil {
		c.mu.Lock()
		c.nodesUnavailable = true
		c.mu.Unlock()
		c.addError(constant.SourceK8s, fmt.Errorf("nodes: %w", err))
		return
	}

	result := clusterModel.Nodes{Total: len(nodes)}
	for _, n := range nodes {
		result.CPUMillis += n.CPUMillis
		result.MemoryBytes += n.MemoryBytes
		problems := make([]string, 0, 2)
		if n.Ready {
			result.Ready++
		} else {
			problems = append(problems, "NotReady")
		}
		if n.Unschedulable {
			problems = append(problems, "Unschedulable")
		}
		problems = append(problems, n.Pressures...)
		if len(problems) > 0 {
			result.Problems = append(result.Problems, clusterModel.NodeProblem{Name: n.Name, Problems: problems})
		}
	}

	c.mu.Lock()
	c.h.Nodes = result
	c.mu.Unlock()
}

// pods — все поды кластера одним запросом: фазы и проблемные состояния с привязкой к сервису.
func (c *collector) pods(ctx context.Context) {
	pods, err := c.u.k8s.ListPods(ctx, "", "")
	if err != nil {
		c.addError(constant.SourceK8s, fmt.Errorf("pods: %w", err))
		return
	}

	result := clusterModel.Pods{Total: len(pods)}
	since := c.now.Add(-c.h.Window)
	for _, pod := range pods {
		switch pod.Phase {
		case "Running":
			result.Running++
		case "Pending":
			result.Pending++
		case "Failed":
			result.Failed++
		case "Succeeded":
			result.Succeeded++
		}

		service := c.serviceOf(&pod)
		add := func(reason, message, image string, at time.Time) {
			result.ProblemsTotal++
			if len(result.Problems) < c.u.conf.MaxProblemPods {
				result.Problems = append(result.Problems, clusterModel.PodProblem{
					Namespace: pod.Namespace, Pod: pod.Name, Service: service, Image: imageName(image),
					Reason: reason, Message: message, Since: at,
				})
			}
		}

		// упавший под — одна запись: причины контейнеров в message, время — когда
		// завершился последний (контейнеры отдельно не повторяются)
		if pod.Phase == "Failed" {
			failed := lo.Filter(pod.Containers, func(ct k8sModel.PodContainer, _ int) bool {
				return ct.State == "terminated" && ct.Reason != "Completed"
			})
			at := lo.Reduce(failed, func(at time.Time, ct k8sModel.PodContainer, _ int) time.Time {
				return lo.Latest(at, ct.TerminatedAt)
			}, time.Time{})
			if at.IsZero() {
				at = pod.StartedAt
			}
			image := lo.FirstOr(failed, lo.FirstOr(pod.Containers, k8sModel.PodContainer{})).Image
			add("Failed", strings.Join(lo.Map(failed, func(ct k8sModel.PodContainer, _ int) string { return ct.Name + ": " + ct.Reason }), ", "), image, at)
			continue
		}

		if pod.Phase == "Pending" && !pod.StartedAt.IsZero() && c.now.Sub(pod.StartedAt) > pendingGrace {
			add("Pending", "", lo.FirstOr(pod.Containers, k8sModel.PodContainer{}).Image, pod.StartedAt)
		}
		for _, ct := range pod.Containers {
			switch {
			case ct.State != "running" && ct.Reason != "" && ct.Reason != "Completed" && ct.Reason != "ContainerCreating":
				add(ct.Reason, ct.Name, ct.Image, lo.Ternary(ct.State == "terminated", ct.TerminatedAt, ct.LastTerminatedAt))
			case ct.LastTerminationReason == "OOMKilled" && !ct.LastTerminatedAt.Before(since):
				add("OOMKilled", ct.Name, ct.Image, ct.LastTerminatedAt)
			// контейнер уже поднялся, но перезапускался внутри окна: флапающий под виден,
			// даже если опрос не попал в момент back-off (счётчик рестартов — за всю жизнь пода,
			// поэтому фильтр только по времени последнего завершения)
			case ct.Restarts > 0 && !ct.LastTerminatedAt.IsZero() && !ct.LastTerminatedAt.Before(since) &&
				ct.LastTerminationReason != "Completed":
				add(constant.PodProblemRestarting, fmt.Sprintf("%s: рестартов всего %d", ct.Name, ct.Restarts), ct.Image, ct.LastTerminatedAt)
			}
		}
	}

	sort.SliceStable(result.Problems, func(i, j int) bool { return result.Problems[i].Since.After(result.Problems[j].Since) })

	c.mu.Lock()
	c.h.Pods = result
	c.mu.Unlock()
}

// serviceOf — сервис каталога, к которому относится под: по workload'у каталога; под без
// него (Job, созданный оркестратором, — задачи loom и т.п.) — по имени репозитория образа,
// иначе по оркестратору из app.kubernetes.io/managed-by, если такие сервисы есть в каталоге.
func (c *collector) serviceOf(pod *k8sModel.Pod) string {
	for _, w := range c.workloads {
		if w.Namespace == pod.Namespace && strings.HasPrefix(pod.Name, w.Name+"-") {
			return w.ServiceName
		}
	}
	for _, ct := range pod.Containers {
		if ref, err := imageref.Parse(ct.Image); err == nil {
			if _, ok := c.services[ref.RepoName()]; ok {
				return ref.RepoName()
			}
		}
	}
	if _, ok := c.services[pod.Labels[managedByLabel]]; ok {
		return pod.Labels[managedByLabel]
	}
	return ""
}

// managedByLabel — оркестратор, создавший под (loom, Helm…).
const managedByLabel = "app.kubernetes.io/managed-by"

// imageName — образ без тега и digest: версия ничего не говорит о проблеме, а digest длинный.
func imageName(image string) string {
	if image == "" {
		return ""
	}
	ref, err := imageref.Parse(image)
	if err != nil {
		return image
	}
	return strings.TrimPrefix(ref.Host+"/"+ref.Path, "/")
}

// events — Warning-события за окно по всему кластеру, сгруппированные по причине.
func (c *collector) events(ctx context.Context) {
	events, err := c.u.k8s.ListEvents(ctx, "", c.now.Add(-c.h.Window))
	if err != nil {
		c.addError(constant.SourceK8s, fmt.Errorf("events: %w", err))
		return
	}

	type agg struct {
		count      int
		namespaces map[string]struct{}
		example    string
		lastTS     time.Time
	}
	byReason := make(map[string]*agg, 16)
	for _, e := range events {
		if e.Type != "Warning" {
			continue
		}
		a, ok := byReason[e.Reason]
		if !ok {
			a = &agg{namespaces: map[string]struct{}{}}
			byReason[e.Reason] = a
		}
		a.count += int(max(e.Count, 1))
		a.namespaces[e.Namespace] = struct{}{}
		if e.LastTS.After(a.lastTS) {
			a.lastTS = e.LastTS
			a.example = fmt.Sprintf("%s/%s %s: %s", e.Namespace, e.ObjectName, e.ObjectKind, lo.Ellipsis(strings.TrimSpace(e.Message), 200))
		}
	}

	reasons := lo.MapToSlice(byReason, func(reason string, a *agg) clusterModel.EventReason {
		return clusterModel.EventReason{Reason: reason, Count: a.count, Namespaces: len(a.namespaces), Example: a.example, LastTS: a.lastTS}
	})
	sort.Slice(reasons, func(i, j int) bool {
		if reasons[i].Count != reasons[j].Count {
			return reasons[i].Count > reasons[j].Count
		}
		return reasons[i].Reason < reasons[j].Reason
	})
	if len(reasons) > c.u.conf.MaxEventReasons {
		reasons = reasons[:c.u.conf.MaxEventReasons]
	}

	c.mu.Lock()
	c.h.EventReasons = reasons
	c.mu.Unlock()
}

// alerts — активные алерты: не относящиеся ни к одному сервису каталога считаются инфраструктурными.
// Служебные (Watchdog) не показываются; алерты с одним именем в одном namespace сливаются в один
// со счётчиком (десятки KubeJobFailed по упавшим Job'ам — одна строка), самый ранний — первым.
func (c *collector) alerts(ctx context.Context) {
	if c.u.alertmanager == nil {
		c.addError(constant.SourceAlertmanager, errs.Err("not configured"))
		return
	}

	alerts, err := c.u.alertmanager.ListAlerts(ctx)
	if err != nil {
		c.addError(constant.SourceAlertmanager, err)
		return
	}

	names := lo.Uniq(lo.FlatMap(c.workloads, func(w *workloadModel.Main, _ int) []string { return []string{w.Name, w.ServiceName} }))

	type alertKey struct{ name, namespace, severity string }
	groups := make(map[alertKey][]alertmanagerModel.Alert)
	order := make([]alertKey, 0)
	serviceActive := 0
	for _, a := range alerts {
		if a.State != "active" || c.u.baseline.IsMonitoringAlert(a.Labels) {
			continue
		}
		if c.u.baseline.AlertMatches(a.Labels, names) {
			serviceActive++
			continue
		}
		key := alertKey{name: a.Name(), namespace: a.Labels["namespace"], severity: a.Severity()}
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], a)
	}

	infra := lo.Map(order, func(key alertKey, _ int) snapshotModel.Alert {
		group := groups[key]
		sort.SliceStable(group, func(i, j int) bool { return group[i].StartsAt.Before(group[j].StartsAt) })
		first := group[0]
		alert := snapshotModel.Alert{
			Name: key.name, Severity: key.severity, State: first.State, StartsAt: first.StartsAt,
			Summary: lo.CoalesceOrEmpty(first.Annotations["summary"], first.Annotations["description"], first.Annotations["message"]),
			Labels:  c.u.baseline.MergeAlertLabels(lo.Map(group, func(a alertmanagerModel.Alert, _ int) map[string]string { return c.u.baseline.AlertLabels(a.Labels) })),
		}
		if len(group) > 1 {
			alert.Count = len(group)
		}
		return alert
	})
	sort.SliceStable(infra, func(i, j int) bool {
		if (infra[i].Severity == "critical") != (infra[j].Severity == "critical") {
			return infra[i].Severity == "critical"
		}
		return infra[i].StartsAt.After(infra[j].StartsAt)
	})
	if len(infra) > c.u.conf.MaxInfraAlerts {
		infra = infra[:c.u.conf.MaxInfraAlerts]
	}

	c.mu.Lock()
	c.h.InfraAlerts = infra
	c.h.ServiceAlertsActive = serviceActive
	c.mu.Unlock()
}

// metrics — загрузка кластера в сравнении с часом назад и вчера.
func (c *collector) metrics(ctx context.Context) {
	if c.u.prometheus == nil {
		c.addError(constant.SourcePrometheus, errs.Err("not configured"))
		return
	}

	metrics := make([]snapshotModel.Metric, len(c.u.conf.Metrics))
	var firstErr error

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(8)
	for i, def := range c.u.conf.Metrics {
		metrics[i].MetricDef = def
		targets := []struct {
			at   time.Time
			dest **float64
		}{{c.now, &metrics[i].Current}, {c.now.Add(-time.Hour), &metrics[i].HourAgo}, {c.now.Add(-24 * time.Hour), &metrics[i].SameTimeYesterday}}
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

	if firstErr != nil {
		c.addError(constant.SourcePrometheus, firstErr)
	}
	for i := range metrics {
		c.u.baseline.ApplyBaseline(&metrics[i])
	}

	c.mu.Lock()
	c.h.Metrics = metrics
	c.mu.Unlock()
}

var urlRe = regexp.MustCompile(`https?://[^\s"]+`)

func compactError(err error) string {
	return urlRe.ReplaceAllString(err.Error(), "<url>")
}
