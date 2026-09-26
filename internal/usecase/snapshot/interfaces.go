package snapshot

import (
	"context"

	"time"

	dependencyModel "github.com/rendau/pulse/internal/domain/dependency/model"
	eventModel "github.com/rendau/pulse/internal/domain/event/model"
	logsModel "github.com/rendau/pulse/internal/domain/logs/model"
	snapshotModel "github.com/rendau/pulse/internal/domain/snapshot/model"
	svcModel "github.com/rendau/pulse/internal/domain/svc/model"
	workloadModel "github.com/rendau/pulse/internal/domain/workload/model"
	alertmanagerModel "github.com/rendau/pulse/internal/service/alertmanager/model"
	k8sModel "github.com/rendau/pulse/internal/service/k8s/model"
	prometheusModel "github.com/rendau/pulse/internal/service/prometheus/model"
	"github.com/rendau/pulse/internal/usecase/snapshot/model"
)

type SnapshotI interface {
	Snapshot(ctx context.Context, service string, window time.Duration) (*snapshotModel.Snapshot, error)
	QueryMetrics(ctx context.Context, req *model.QueryMetricsReq) (*model.QueryMetricsResult, error)
}

// ports

type svcServiceI interface {
	GetOrSuggest(ctx context.Context, name string) (*svcModel.Main, error)
}

type workloadServiceI interface {
	List(ctx context.Context, pars *workloadModel.ListReq) ([]*workloadModel.Main, int64, error)
}

type dependencyServiceI interface {
	List(ctx context.Context, pars *dependencyModel.ListReq) ([]*dependencyModel.Main, int64, error)
}

type k8sClientI interface {
	ListPods(ctx context.Context, namespace, selector string) ([]k8sModel.Pod, error)
	ListEvents(ctx context.Context, namespace string, since time.Time) ([]k8sModel.Event, error)
}

// PrometheusI и AlertmanagerI экспортированы: источники опциональны, композиционный корень
// передаёт nil, когда источник не сконфигурирован.
type PrometheusI interface {
	Query(ctx context.Context, promql string, at time.Time) ([]prometheusModel.Sample, error)
	QueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) ([]prometheusModel.Series, error)
}

type AlertmanagerI interface {
	ListAlerts(ctx context.Context) ([]alertmanagerModel.Alert, error)
}

// LogsI — usecase логов (top_errors); nil, когда Loki не сконфигурирован.
// SelfReportI экспортирован: самоотчёт сервиса (ручка состояния по манифесту); nil — выключено.
type SelfReportI interface {
	Report(ctx context.Context, service *svcModel.Main, workloads []*workloadModel.Main) (*snapshotModel.SelfReport, []snapshotModel.SourceError)
}

type LogsI interface {
	TopErrors(ctx context.Context, service *svcModel.Main, workloads []*workloadModel.Main, window time.Duration, top int) ([]logsModel.Pattern, error)
}

type eventServiceI interface {
	FromCluster(e eventModel.ClusterEvent, service string) (eventModel.Event, bool)
	FromTermination(t eventModel.ContainerTermination, service string) (eventModel.Event, bool)
}

type rulesServiceI interface {
	ComputeHealth(snap *snapshotModel.Snapshot, podsUnavailable bool) string
	SummaryHints(snap *snapshotModel.Snapshot, now time.Time) []string
	ApplyBaseline(m *snapshotModel.Metric)
	AlertMatches(labels map[string]string, names []string) bool
	AlertLabels(labels map[string]string) map[string]string
	AlertSeverity(s string) string
}
