// Package cluster — «у нас всё лежит или только платежи»: здоровье кластера в целом.
package cluster

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

	"github.com/rendau/pulse/internal/constant"
	clusterModel "github.com/rendau/pulse/internal/domain/cluster/model"
	snapshotModel "github.com/rendau/pulse/internal/domain/snapshot/model"
	workloadModel "github.com/rendau/pulse/internal/domain/workload/model"
	"github.com/rendau/pulse/internal/errs"
	alertmanagerModel "github.com/rendau/pulse/internal/service/alertmanager/model"
	k8sModel "github.com/rendau/pulse/internal/service/k8s/model"
	prometheusModel "github.com/rendau/pulse/internal/service/prometheus/model"
	"github.com/rendau/pulse/internal/util/imageref"
	"github.com/rendau/pulse/internal/util/podname"
	"github.com/rendau/pulse/internal/util/window"
)

// Config — лимиты (из yaml-правил).
type Config struct {
	Deadline        time.Duration
	MaxProblemPods  int
	MaxEventReasons int
	MaxInfraAlerts  int
	MaxLogServices  int
	Metrics         []snapshotModel.MetricDef
	Public          PublicConfig
}

type Usecase struct {
	conf Config

	svc          svcServiceI
	self         SelfReportI
	workload     workloadServiceI
	k8s          k8sClientI
	prometheus   PrometheusI
	alertmanager AlertmanagerI
	logs         LogsI
	ruto         RutoI
	depend       dependencyServiceI
	rules        rulesServiceI
	baseline     baselineServiceI
}

func New(conf Config, svc svcServiceI, self SelfReportI, workload workloadServiceI, k8s k8sClientI, prometheus PrometheusI, alertmanager AlertmanagerI, logs LogsI, ruto RutoI, depend dependencyServiceI, rules rulesServiceI, baseline baselineServiceI) *Usecase {
	if conf.Deadline <= 0 {
		conf.Deadline = time.Minute
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
	if conf.MaxLogServices <= 0 {
		conf.MaxLogServices = 10
	}
	if conf.Public.MaxApps <= 0 {
		conf.Public.MaxApps = 20
	}
	if conf.Public.LogLines <= 0 {
		conf.Public.LogLines = 1000
	}
	return &Usecase{conf: conf, svc: svc, self: self, workload: workload, k8s: k8s, prometheus: prometheus, alertmanager: alertmanager, logs: logs, ruto: ruto, depend: depend, rules: rules, baseline: baseline}
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
	// события — после подов: объект события привязывается к сервису через свой под
	eg.Go(func() error { c.pods(egCtx); c.events(egCtx); return nil })
	eg.Go(func() error { c.alerts(egCtx); return nil })
	eg.Go(func() error { c.metrics(egCtx); return nil })
	eg.Go(func() error { c.logErrors(egCtx); return nil })
	eg.Go(func() error { c.selfReports(egCtx); return nil })
	if c.publicEnabled() {
		eg.Go(func() error { c.publicTraffic(egCtx); return nil })
		eg.Go(func() error { c.gatewayErrors(egCtx); return nil })
		eg.Go(func() error { c.publicRoutes(egCtx); return nil })
	}
	_ = eg.Wait()
	if c.publicEnabled() {
		c.publicApps(c.readyPods)
	}

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

	// podServices — сервис каждого пода по namespace (заполняет pods, читает events)
	podServices map[string]map[string]string
	// readyPods — готовых подов по сервису (заполняет pods; nil — поды недоступны)
	readyPods map[string]int
	// public — сырьё для проблем публичных приложений gateway (public.go)
	public public

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
	podServices := make(map[string]map[string]string, 16)
	readyPods := make(map[string]int, 64)
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
		if podServices[pod.Namespace] == nil {
			podServices[pod.Namespace] = map[string]string{}
		}
		podServices[pod.Namespace][pod.Name] = service
		if pod.Ready && service != "" {
			readyPods[service]++
		}

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
	c.podServices = podServices
	c.readyPods = readyPods
	c.mu.Unlock()
}

// serviceOf — сервис каталога, к которому относится под: по workload'у каталога; под без
// него (Job, созданный оркестратором: Argo Workflows, Airflow и т.п.) — по ownerService.
func (c *collector) serviceOf(pod *k8sModel.Pod) string {
	if w := workloadOf(c.workloads, pod.Namespace, pod.Name, podname.Owner); w != nil {
		return w.ServiceName
	}
	return c.ownerService(lo.Map(pod.Containers, func(ct k8sModel.PodContainer, _ int) string { return ct.Image }), pod.Labels)
}

// workloadOf — workload каталога в namespace, которому принадлежит под или объект:
// по правилам именования вида (podname), а не по префиксу «имя-» — иначе под pulse-agent-…
// достался бы pulse.
func workloadOf(workloads []*workloadModel.Main, namespace, name string, owner func(string, []podname.Workload) int) *workloadModel.Main {
	local := lo.Filter(workloads, func(w *workloadModel.Main, _ int) bool { return w.Namespace == namespace })
	i := owner(name, lo.Map(local, func(w *workloadModel.Main, _ int) podname.Workload {
		return podname.Workload{Kind: w.Kind, Name: w.Name}
	}))
	if i < 0 {
		return nil
	}
	return local[i]
}

// ownerService — сервис объекта без workload'а каталога (под, Job): по имени репозитория
// образа, иначе по оркестратору из app.kubernetes.io/managed-by — если такой сервис есть
// в каталоге. Стандартные метки Kubernetes, без знания о конкретных оркестраторах.
func (c *collector) ownerService(images []string, labels map[string]string) string {
	for _, image := range images {
		if ref, err := imageref.Parse(image); err == nil {
			if _, ok := c.services[ref.RepoName()]; ok {
				return ref.RepoName()
			}
		}
	}
	if _, ok := c.services[labels[managedByLabel]]; ok {
		return labels[managedByLabel]
	}
	return ""
}

// jobServices — сервисы Job'ов, которые не нашлись по подам (поды уже удалены): Job'ы
// читаются только в нужных namespace'ах. Ошибка (нет прав на jobs и т.п.) — не ошибка
// ответа: привязка событий к сервисам — уточнение, без неё ответ полный.
func (c *collector) jobServices(ctx context.Context, want map[string]map[string]struct{}) map[string]map[string]string {
	result := make(map[string]map[string]string, len(want))
	for namespace, names := range want {
		jobs, err := c.u.k8s.ListJobs(ctx, namespace)
		if err != nil {
			slog.Warn("cluster health: list jobs", "namespace", namespace, "error", err)
			continue
		}
		result[namespace] = map[string]string{}
		for _, job := range jobs {
			if _, ok := names[job.Name]; ok {
				result[namespace][job.Name] = c.ownerService(lo.Map(job.Containers, func(ct k8sModel.Container, _ int) string { return ct.Image }), job.Labels)
			}
		}
	}
	return result
}

// objectService — сервис объекта Warning-события: сам под; объект workload'а каталога
// (Deployment, ReplicaSet по префиксу имени); Job или другой владелец — по его подам
// (имя пода = имя владельца + суффикс). Не нашлось — пусто.
func (c *collector) objectService(namespace, name string) string {
	c.mu.Lock()
	pods := c.podServices[namespace]
	c.mu.Unlock()

	if service := pods[name]; service != "" {
		return service
	}
	if w := workloadOf(c.workloads, namespace, name, podname.ObjectOwner); w != nil {
		return w.ServiceName
	}
	for pod, service := range pods {
		if service != "" && strings.HasPrefix(pod, name+"-") {
			return service
		}
	}
	return ""
}

// eventExampleLen — сколько символов сообщения события в примере: у сетевых ошибок
// (FailedCreatePodSandBox) суть в конце длинной строки.
const eventExampleLen = 600

// managedByLabel — оркестратор, создавший под (Argo Workflows, Helm…).
const managedByLabel = "app.kubernetes.io/managed-by"

// imageName — образ без тега и digest: версия ничего не говорит о проблеме, а digest длинный.
func imageName(image string) string {
	// «sha256:…» без имени (kubelet не знает имени образа) — не образ, а только digest
	if image == "" || strings.HasPrefix(image, "sha256:") {
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
		services   map[string]struct{}
		example    string
		lastTS     time.Time
	}
	warnings := lo.Filter(events, func(e k8sModel.Event, _ int) bool { return e.Type == "Warning" })

	// сервис объекта каждого события; Job без подов — дозапросом Job'ов
	services := make([]string, len(warnings))
	unresolvedJobs := map[string]map[string]struct{}{}
	for i, e := range warnings {
		services[i] = c.objectService(e.Namespace, e.ObjectName)
		if services[i] == "" && e.ObjectKind == "Job" {
			if unresolvedJobs[e.Namespace] == nil {
				unresolvedJobs[e.Namespace] = map[string]struct{}{}
			}
			unresolvedJobs[e.Namespace][e.ObjectName] = struct{}{}
		}
	}
	if len(unresolvedJobs) > 0 {
		jobs := c.jobServices(ctx, unresolvedJobs)
		for i, e := range warnings {
			if services[i] == "" && e.ObjectKind == "Job" {
				services[i] = jobs[e.Namespace][e.ObjectName]
			}
		}
	}

	byReason := make(map[string]*agg, 16)
	for i, e := range warnings {
		a, ok := byReason[e.Reason]
		if !ok {
			a = &agg{namespaces: map[string]struct{}{}, services: map[string]struct{}{}}
			byReason[e.Reason] = a
		}
		a.count += int(max(e.Count, 1))
		a.namespaces[e.Namespace] = struct{}{}
		if services[i] != "" {
			a.services[services[i]] = struct{}{}
		}
		if e.LastTS.After(a.lastTS) {
			a.lastTS = e.LastTS
			a.example = fmt.Sprintf("%s/%s %s: %s", e.Namespace, e.ObjectName, e.ObjectKind, lo.Ellipsis(strings.TrimSpace(e.Message), eventExampleLen))
		}
	}

	reasons := lo.MapToSlice(byReason, func(reason string, a *agg) clusterModel.EventReason {
		services := lo.Keys(a.services)
		sort.Strings(services)
		return clusterModel.EventReason{Reason: reason, Count: a.count, Namespaces: len(a.namespaces), Services: services, Example: a.example, LastTS: a.lastTS}
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

// logErrors — ошибки в логах всего кластера по сервисам: «есть ли ошибки в логах» без
// перебора сервисов по одному.
func (c *collector) logErrors(ctx context.Context) {
	if c.u.logs == nil {
		return
	}

	result, err := c.u.logs.ClusterErrors(ctx, c.h.Window, c.u.conf.MaxLogServices)
	if err != nil {
		c.addError(constant.SourceLoki, err)
		return
	}

	c.mu.Lock()
	c.h.LogErrors = result
	c.mu.Unlock()
}

var urlRe = regexp.MustCompile(`https?://[^\s"]+`)

func compactError(err error) string {
	return urlRe.ReplaceAllString(err.Error(), "<url>")
}
