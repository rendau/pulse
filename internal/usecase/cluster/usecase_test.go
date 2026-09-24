package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse/internal/constant"
	clusterModel "github.com/mechta-market/pulse/internal/domain/cluster/model"
	clusterService "github.com/mechta-market/pulse/internal/domain/cluster/service"
	snapshotModel "github.com/mechta-market/pulse/internal/domain/snapshot/model"
	snapshotService "github.com/mechta-market/pulse/internal/domain/snapshot/service"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	alertmanagerModel "github.com/mechta-market/pulse/internal/service/alertmanager/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
	prometheusModel "github.com/mechta-market/pulse/internal/service/prometheus/model"
)

type fakeWorkload struct{}

func (fakeWorkload) List(context.Context, *workloadModel.ListReq) ([]*workloadModel.Main, int64, error) {
	return []*workloadModel.Main{
		{Namespace: "prod", Name: "payments-api", ServiceName: "payments-api"},
		{Namespace: "loom", Name: "loom", ServiceName: "loom"},
	}, 2, nil
}

type fakeK8s struct {
	nodes    []k8sModel.Node
	pods     []k8sModel.Pod
	events   []k8sModel.Event
	jobs     []k8sModel.Job
	jobsErr  error
	nodesErr error
}

func (f *fakeK8s) ListNodes(context.Context) ([]k8sModel.Node, error) { return f.nodes, f.nodesErr }
func (f *fakeK8s) ListPods(context.Context, string, string) ([]k8sModel.Pod, error) {
	return f.pods, nil
}
func (f *fakeK8s) ListEvents(context.Context, string, time.Time) ([]k8sModel.Event, error) {
	return f.events, nil
}
func (f *fakeK8s) ListJobs(_ context.Context, namespace string) ([]k8sModel.Job, error) {
	return lo.Filter(f.jobs, func(j k8sModel.Job, _ int) bool { return j.Namespace == namespace }), f.jobsErr
}

type fakeProm struct{}

func (fakeProm) Query(_ context.Context, promql string, at time.Time) ([]prometheusModel.Sample, error) {
	v := 0.9
	if time.Since(at) > 23*time.Hour {
		v = 0.5
	}
	if promql != `1 - avg(rate(node_cpu_seconds_total{mode="idle"}[5m]))` {
		v = 1
	}
	return []prometheusModel.Sample{{Value: v}}, nil
}

type fakeAM struct{ alerts []alertmanagerModel.Alert }

func (f fakeAM) ListAlerts(context.Context) ([]alertmanagerModel.Alert, error) { return f.alerts, nil }

func newUsecase(k8s *fakeK8s, am AlertmanagerI, prom PrometheusI) *Usecase {
	return New(Config{Deadline: 2 * time.Second, Metrics: []snapshotModel.MetricDef{
		{Id: "cluster_cpu_usage_ratio", PromQL: `1 - avg(rate(node_cpu_seconds_total{mode="idle"}[5m]))`, Direction: "lower_is_better"},
	}}, fakeWorkload{}, k8s, prom, am,
		clusterService.New(clusterService.Config{PendingPodsThreshold: 2}),
		snapshotService.New(snapshotService.Config{AnomalyThresholdPct: 30}))
}

func TestHealth_Degraded(t *testing.T) {
	now := time.Now()
	k8s := &fakeK8s{
		nodes: []k8sModel.Node{
			{Name: "n1", Ready: true, CPUMillis: 4000, MemoryBytes: 8 << 30},
			{Name: "n2", Ready: true, Pressures: []string{"MemoryPressure"}, CPUMillis: 4000, MemoryBytes: 8 << 30},
			{Name: "n3", Ready: false, CPUMillis: 4000, MemoryBytes: 8 << 30},
		},
		pods: []k8sModel.Pod{
			{Namespace: "prod", Name: "payments-api-1", Phase: "Running", Ready: true},
			{Namespace: "prod", Name: "payments-api-2", Phase: "Running", Containers: []k8sModel.PodContainer{{Name: "app", State: "waiting", Reason: "CrashLoopBackOff"}}},
			{Namespace: "prod", Name: "other-1", Phase: "Pending", StartedAt: now.Add(-10 * time.Minute)},
			{Namespace: "prod", Name: "fresh-1", Phase: "Pending", StartedAt: now.Add(-10 * time.Second)},
			{Namespace: "batch", Name: "job-1", Phase: "Succeeded"},
		},
		events: []k8sModel.Event{
			{Namespace: "prod", ObjectKind: "Pod", ObjectName: "other-1", Reason: "FailedScheduling", Type: "Warning", Message: "0/3 nodes are available", Count: 12, LastTS: now},
			{Namespace: "dev", ObjectKind: "Pod", ObjectName: "x", Reason: "FailedScheduling", Type: "Warning", Count: 3, LastTS: now.Add(-time.Minute)},
			{Namespace: "prod", ObjectKind: "Pod", ObjectName: "y", Reason: "Pulled", Type: "Normal", LastTS: now},
		},
	}
	am := fakeAM{alerts: []alertmanagerModel.Alert{
		{Labels: map[string]string{"alertname": "NodeDiskFull", "severity": "critical", "instance": "n2"}, State: "active", StartsAt: now},
		{Labels: map[string]string{"alertname": "HighErrorRate", "severity": "critical", "service": "payments-api"}, State: "active", StartsAt: now},
		{Labels: map[string]string{"alertname": "Silenced"}, State: "suppressed"},
		{Labels: map[string]string{"alertname": "Watchdog", "severity": "none"}, State: "active", StartsAt: now},
		// упавшие Job'ы loom: лейблы pod/container/service/job — экспортера kube-state-metrics
		{Labels: map[string]string{"alertname": "KubeJobFailed", "severity": "warning", "namespace": "loom", "job_name": "sync-1", "job": "kube-state-metrics",
			"container": "kube-state-metrics", "pod": "prometheus-kube-state-metrics-1", "service": "prometheus-kube-state-metrics", "instance": "10.0.0.1:8080"}, State: "active", StartsAt: now.Add(-time.Hour)},
		{Labels: map[string]string{"alertname": "KubeJobFailed", "severity": "warning", "namespace": "loom", "job_name": "sync-2", "job": "kube-state-metrics",
			"container": "kube-state-metrics", "pod": "prometheus-kube-state-metrics-1", "service": "prometheus-kube-state-metrics"}, State: "active", StartsAt: now.Add(-30 * time.Minute)},
	}}

	h, err := newUsecase(k8s, am, fakeProm{}).Health(context.Background(), time.Hour)
	require.NoError(t, err)

	assert.Equal(t, snapshotModel.HealthDegraded, h.Health)
	assert.Empty(t, h.Errors)

	assert.Equal(t, 3, h.Nodes.Total)
	assert.Equal(t, 2, h.Nodes.Ready)
	assert.Len(t, h.Nodes.Problems, 2)
	assert.Equal(t, int64(12000), h.Nodes.CPUMillis)

	assert.Equal(t, 5, h.Pods.Total)
	assert.Equal(t, 2, h.Pods.Pending)
	reasons := lo.Map(h.Pods.Problems, func(p clusterPodProblem, _ int) string { return p.Reason })
	assert.ElementsMatch(t, []string{"CrashLoopBackOff", "Pending"}, reasons, "свежий pending в grace-период не проблема")
	crash, _ := lo.Find(h.Pods.Problems, func(p clusterPodProblem) bool { return p.Reason == "CrashLoopBackOff" })
	assert.Equal(t, "payments-api", crash.Service)

	require.Len(t, h.EventReasons, 1)
	assert.Equal(t, "FailedScheduling", h.EventReasons[0].Reason)
	assert.Equal(t, 15, h.EventReasons[0].Count)
	assert.Equal(t, 2, h.EventReasons[0].Namespaces)

	require.Len(t, h.InfraAlerts, 2, "алерт сервиса, подавленный и Watchdog не попадают в инфра; повторы слиты")
	assert.Equal(t, "NodeDiskFull", h.InfraAlerts[0].Name)
	assert.NotContains(t, h.InfraAlerts[0].Labels, "instance")
	jobs := h.InfraAlerts[1]
	assert.Equal(t, "KubeJobFailed", jobs.Name)
	assert.Equal(t, 2, jobs.Count)
	assert.Equal(t, now.Add(-time.Hour), jobs.StartsAt, "самый ранний")
	assert.Equal(t, map[string]string{"alertname": "KubeJobFailed", "severity": "warning", "namespace": "loom", "job_name": "sync-1, sync-2", "job": "kube-state-metrics"}, jobs.Labels)
	assert.Equal(t, 1, h.ServiceAlertsActive)

	require.Len(t, h.Metrics, 1)
	assert.True(t, h.Metrics[0].Anomaly, "CPU вырос на 80% относительно вчера")

	assert.NotEmpty(t, h.SummaryHints)
	t.Logf("hints: %v", h.SummaryHints)
}

func TestHealth_DownAndUnknown(t *testing.T) {
	k8s := &fakeK8s{nodes: []k8sModel.Node{{Name: "n1", Ready: false}, {Name: "n2", Ready: false}, {Name: "n3", Ready: true}}}
	h, err := newUsecase(k8s, nil, nil).Health(context.Background(), time.Hour)
	require.NoError(t, err)
	assert.Equal(t, snapshotModel.HealthDown, h.Health, "2 из 3 нод не готовы")
	sources := lo.Map(h.Errors, func(e snapshotModel.SourceError, _ int) string { return e.Source })
	assert.ElementsMatch(t, []string{constant.SourceAlertmanager, constant.SourcePrometheus}, sources)

	h, err = newUsecase(&fakeK8s{nodesErr: errors.New("forbidden")}, nil, nil).Health(context.Background(), time.Hour)
	require.NoError(t, err)
	assert.Equal(t, snapshotModel.HealthUnknown, h.Health)
	assert.Contains(t, lo.Map(h.Errors, func(e snapshotModel.SourceError, _ int) string { return e.Source }), constant.SourceK8s)
}

// TestHealth_RestartingPods: контейнер уже поднялся, но перезапускался внутри окна — под
// проблемный (иначе флапающий под виден, только если опрос попал в момент back-off).
func TestHealth_RestartingPods(t *testing.T) {
	now := time.Now()
	k8s := &fakeK8s{
		nodes: []k8sModel.Node{{Name: "n1", Ready: true, CPUMillis: 4000, MemoryBytes: 8 << 30}},
		pods: []k8sModel.Pod{
			{Namespace: "prod", Name: "payments-api-1", Phase: "Running", Ready: true, Containers: []k8sModel.PodContainer{
				{Name: "app", State: "running", Restarts: 27, LastTerminationReason: "Error", LastTerminatedAt: now.Add(-10 * time.Minute)},
			}},
			{Namespace: "prod", Name: "payments-api-2", Phase: "Running", Ready: true, Containers: []k8sModel.PodContainer{
				{Name: "app", State: "running", Restarts: 68, LastTerminationReason: "Error", LastTerminatedAt: now.Add(-5 * time.Hour)},
			}},
			{Namespace: "batch", Name: "job-1", Phase: "Running", Ready: true, Containers: []k8sModel.PodContainer{
				{Name: "app", State: "running", Restarts: 3, LastTerminationReason: "Completed", LastTerminatedAt: now.Add(-time.Minute)},
			}},
			{Namespace: "prod", Name: "no-restarts-1", Phase: "Running", Ready: true, Containers: []k8sModel.PodContainer{
				{Name: "app", State: "running"},
			}},
		},
	}

	h, err := newUsecase(k8s, nil, nil).Health(context.Background(), time.Hour)
	require.NoError(t, err)

	require.Len(t, h.Pods.Problems, 1, "рестарт вне окна и штатное завершение проблемой не считаются")
	assert.Equal(t, constant.PodProblemRestarting, h.Pods.Problems[0].Reason)
	assert.Equal(t, "payments-api-1", h.Pods.Problems[0].Pod)
	assert.Contains(t, h.Pods.Problems[0].Message, "27")
	assert.Equal(t, snapshotModel.HealthHealthy, h.Health, "один проблемный под ниже порога деградации")
}

// TestHealth_ProblemPodsThreshold: с порога проблемных подов кластер деградировал.
func TestHealth_ProblemPodsThreshold(t *testing.T) {
	pods := lo.Map(lo.Range(3), func(i int, _ int) k8sModel.Pod {
		return k8sModel.Pod{
			Namespace: "prod", Name: fmt.Sprintf("payments-api-%d", i), Phase: "Running",
			Containers: []k8sModel.PodContainer{{Name: "app", State: "waiting", Reason: "ImagePullBackOff"}},
		}
	})
	k8s := &fakeK8s{
		nodes: []k8sModel.Node{{Name: "n1", Ready: true, CPUMillis: 4000, MemoryBytes: 8 << 30}},
		pods:  pods,
	}

	h, err := newUsecase(k8s, nil, nil).Health(context.Background(), time.Hour)
	require.NoError(t, err)

	assert.Equal(t, 3, h.Pods.ProblemsTotal)
	assert.Equal(t, snapshotModel.HealthDegraded, h.Health)
	assert.Contains(t, strings.Join(h.SummaryHints, "\n"), "ImagePullBackOff")
}

// TestHealth_FailedJobPods: упавший под — одна запись со временем завершения контейнера;
// под Job'а без workload'а каталога привязан к оркестратору (managed-by), образ — без digest.
func TestHealth_FailedJobPods(t *testing.T) {
	now := time.Now()
	finished := now.Add(-3 * time.Minute)
	k8s := &fakeK8s{
		nodes: []k8sModel.Node{{Name: "n1", Ready: true}},
		pods: []k8sModel.Pod{
			{
				Namespace: "loom", Name: "lt-zeon-sync-1-abc-x1", Phase: "Failed", StartedAt: now.Add(-10 * time.Minute),
				Labels: map[string]string{"app.kubernetes.io/managed-by": "loom"},
				Containers: []k8sModel.PodContainer{{
					Name: "task", Image: "ghcr.io/mechta-market/airflow-dags/dags@sha256:219acc66d359bc23a8fdc4494c3d299e73d403d5c0074705c6b5cf2716d1f464",
					State: "terminated", Reason: "Error", TerminatedAt: finished,
				}},
			},
			// оркестратор не из каталога — сервис не угадываем
			{
				Namespace: "batch", Name: "job-2", Phase: "Failed", StartedAt: now.Add(-time.Minute),
				Labels:     map[string]string{"app.kubernetes.io/managed-by": "Helm"},
				Containers: []k8sModel.PodContainer{{Name: "app", Image: "busybox", State: "terminated", Reason: "Error"}},
			},
		},
	}

	h, err := newUsecase(k8s, nil, nil).Health(context.Background(), time.Hour)
	require.NoError(t, err)

	require.Len(t, h.Pods.Problems, 2, "под не дублируется записью контейнера")
	assert.Equal(t, 2, h.Pods.ProblemsTotal)

	byPod := lo.KeyBy(h.Pods.Problems, func(p clusterModel.PodProblem) string { return p.Pod })
	job := byPod["lt-zeon-sync-1-abc-x1"]
	assert.Equal(t, "lt-zeon-sync-1-abc-x1", job.Pod)
	assert.Equal(t, "Failed", job.Reason)
	assert.Equal(t, "task: Error", job.Message)
	assert.Equal(t, "loom", job.Service)
	assert.Equal(t, "ghcr.io/mechta-market/airflow-dags/dags", job.Image)
	assert.True(t, finished.Equal(job.Since))

	other := byPod["job-2"]
	assert.Empty(t, other.Service)
	assert.False(t, other.Since.IsZero(), "нет времени завершения — время старта пода")
}

// TestHealth_EventServices: Warning-события привязаны к сервисам — через под, workload
// каталога или поды владельца (Job); объект без пода и workload'а — без сервиса.
func TestHealth_EventServices(t *testing.T) {
	now := time.Now()
	k8s := &fakeK8s{
		nodes: []k8sModel.Node{{Name: "n1", Ready: true}},
		pods: []k8sModel.Pod{
			{Namespace: "loom", Name: "lt-sync-1-abc-x1", Phase: "Succeeded", Labels: map[string]string{"app.kubernetes.io/managed-by": "loom"}},
			{Namespace: "prod", Name: "payments-api-7d9f-q2", Phase: "Running", Ready: true},
		},
		events: []k8sModel.Event{
			{Namespace: "loom", ObjectKind: "Job", ObjectName: "lt-sync-1-abc", Reason: "BackoffLimitExceeded", Type: "Warning", Count: 1, LastTS: now},
			{Namespace: "loom", ObjectKind: "Pod", ObjectName: "lt-sync-1-abc-x1", Reason: "FailedCreatePodSandBox", Type: "Warning", Count: 1, LastTS: now},
			{Namespace: "prod", ObjectKind: "Deployment", ObjectName: "payments-api", Reason: "BackoffLimitExceeded", Type: "Warning", Count: 1, LastTS: now},
			{Namespace: "dev", ObjectKind: "Pod", ObjectName: "gone-1", Reason: "FailedCreatePodSandBox", Type: "Warning", Count: 1, LastTS: now},
		},
	}

	h, err := newUsecase(k8s, nil, nil).Health(context.Background(), time.Hour)
	require.NoError(t, err)

	byReason := lo.KeyBy(h.EventReasons, func(r clusterModel.EventReason) string { return r.Reason })
	assert.Equal(t, []string{"loom", "payments-api"}, byReason["BackoffLimitExceeded"].Services)
	assert.Equal(t, []string{"loom"}, byReason["FailedCreatePodSandBox"].Services)
}

// TestHealth_EventServices_JobWithoutPods: поды Job'а уже удалены — владелец по самому Job'у
// (managed-by); нет прав на jobs — событие без сервиса, но ответ без ошибки.
func TestHealth_EventServices_JobWithoutPods(t *testing.T) {
	now := time.Now()
	newK8s := func(jobsErr error) *fakeK8s {
		return &fakeK8s{
			nodes: []k8sModel.Node{{Name: "n1", Ready: true}},
			events: []k8sModel.Event{
				{Namespace: "loom", ObjectKind: "Job", ObjectName: "lt-sync-9-def", Reason: "BackoffLimitExceeded", Type: "Warning", Count: 1, LastTS: now},
			},
			jobs: []k8sModel.Job{
				{Namespace: "loom", Name: "lt-sync-9-def", Labels: map[string]string{"app.kubernetes.io/managed-by": "loom"}, Images: []string{"ghcr.io/org/dags/dags:latest"}},
				{Namespace: "loom", Name: "other", Labels: map[string]string{"app.kubernetes.io/managed-by": "loom"}},
			},
			jobsErr: jobsErr,
		}
	}

	h, err := newUsecase(newK8s(nil), nil, nil).Health(context.Background(), time.Hour)
	require.NoError(t, err)
	require.Len(t, h.EventReasons, 1)
	assert.Equal(t, []string{"loom"}, h.EventReasons[0].Services)

	h, err = newUsecase(newK8s(errors.New("jobs is forbidden")), nil, nil).Health(context.Background(), time.Hour)
	require.NoError(t, err)
	require.Len(t, h.EventReasons, 1)
	assert.Empty(t, h.EventReasons[0].Services)
	assert.NotContains(t, lo.Map(h.Errors, func(e snapshotModel.SourceError, _ int) string { return e.Source }), constant.SourceK8s,
		"без прав на jobs ответ полный, только без привязки")
}
