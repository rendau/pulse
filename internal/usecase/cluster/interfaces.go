package cluster

import (
	"context"
	"time"

	clusterModel "github.com/mechta-market/pulse/internal/domain/cluster/model"
	logsModel "github.com/mechta-market/pulse/internal/domain/logs/model"
	snapshotModel "github.com/mechta-market/pulse/internal/domain/snapshot/model"
	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	alertmanagerModel "github.com/mechta-market/pulse/internal/service/alertmanager/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
	prometheusModel "github.com/mechta-market/pulse/internal/service/prometheus/model"
)

type ClusterI interface {
	Health(ctx context.Context, window time.Duration) (*clusterModel.Health, error)
}

// ports

type workloadServiceI interface {
	List(ctx context.Context, pars *workloadModel.ListReq) ([]*workloadModel.Main, int64, error)
}

type k8sClientI interface {
	ListNodes(ctx context.Context) ([]k8sModel.Node, error)
	ListPods(ctx context.Context, namespace, selector string) ([]k8sModel.Pod, error)
	ListEvents(ctx context.Context, namespace string, since time.Time) ([]k8sModel.Event, error)
	ListJobs(ctx context.Context, namespace string) ([]k8sModel.Job, error)
}

// PrometheusI и AlertmanagerI экспортированы: источники опциональны (nil, если не сконфигурированы).
type PrometheusI interface {
	Query(ctx context.Context, promql string, at time.Time) ([]prometheusModel.Sample, error)
}

type AlertmanagerI interface {
	ListAlerts(ctx context.Context) ([]alertmanagerModel.Alert, error)
}

// LogsI — узкий порт к usecase логов (исключение из правила «usecase не ходит в соседний»,
// как у снапшота): селектор кластера, привязка подов к сервисам и паттерны — там.
type LogsI interface {
	ClusterErrors(ctx context.Context, window time.Duration, top int) (*logsModel.ClusterErrors, error)
}

type svcServiceI interface {
	List(ctx context.Context, pars *svcModel.ListReq) ([]*svcModel.Main, int64, error)
}

// SelfReportI экспортирован: самоотчёт сервиса по манифесту (service/selfreport); nil — выключено.
type SelfReportI interface {
	Report(ctx context.Context, service *svcModel.Main, workloads []*workloadModel.Main) (*snapshotModel.SelfReport, []snapshotModel.SourceError)
}

type rulesServiceI interface {
	ComputeHealth(h *clusterModel.Health, nodesUnavailable bool) string
	SummaryHints(h *clusterModel.Health, now time.Time) []string
}

type baselineServiceI interface {
	ApplyBaseline(m *snapshotModel.Metric)
	AlertMatches(labels map[string]string, names []string) bool
	AlertLabels(labels map[string]string) map[string]string
	MergeAlertLabels(all []map[string]string) map[string]string
	IsMonitoringAlert(labels map[string]string) bool
	SelfHints(self *snapshotModel.SelfReport, now time.Time) []string
}
