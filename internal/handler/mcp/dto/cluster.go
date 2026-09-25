package dto

import (
	"time"

	"github.com/samber/lo"

	clusterModel "github.com/mechta-market/pulse/internal/domain/cluster/model"
	logsModel "github.com/mechta-market/pulse/internal/domain/logs/model"
	"github.com/mechta-market/pulse/internal/util/tz"
	"github.com/mechta-market/pulse/internal/util/window"
)

type GetClusterHealthReq struct {
	Window string `json:"window,omitempty" jsonschema:"окно для событий и OOM: 15m, 1h (по умолчанию), 24h"`
}

type ClusterHealthRep struct {
	GeneratedAt         time.Time     `json:"generated_at"`
	Window              string        `json:"window"`
	Health              string        `json:"health" jsonschema:"healthy | degraded | down | unknown — по фиксированным правилам"`
	SummaryHints        []string      `json:"summary_hints"`
	Nodes               ClusterNodes  `json:"nodes"`
	Pods                ClusterPods   `json:"pods"`
	EventReasons        []EventReason `json:"warning_events_by_reason"`
	InfraAlerts         []Alert       `json:"infra_alerts" jsonschema:"активные алерты, не привязанные к сервисам каталога"`
	ServiceAlertsActive int           `json:"service_alerts_active"`
	Metrics             []Metric      `json:"metrics"`
	LogErrors           *LogErrors    `json:"log_errors,omitempty" jsonschema:"ошибки в логах всего кластера по сервисам; нет — логи недоступны (см. errors)"`
	Errors              []SourceError `json:"errors"`
}

type LogErrors struct {
	Window        string            `json:"window"`
	Total         int               `json:"total" jsonschema:"error-строк во всех логах кластера за окно"`
	Services      []ServiceLogError `json:"services" jsonschema:"сервисы с наибольшим числом ошибок; пустой список — ошибок нет"`
	ServicesTotal int               `json:"services_total"`
}

type ServiceLogError struct {
	Service   string    `json:"service,omitempty" jsonschema:"пусто — под не из каталога, смотри namespace"`
	Namespace string    `json:"namespace"`
	Count     int       `json:"count"`
	TopError  string    `json:"top_error,omitempty" jsonschema:"самая частая ошибка (шаблон); подробности — query_logs(service, level=error)"`
	LastSeen  time.Time `json:"last_seen,omitzero"`
}

type ClusterNodes struct {
	Total     int           `json:"total"`
	Ready     int           `json:"ready"`
	Problems  []NodeProblem `json:"problems" jsonschema:"ноды с проблемами (NotReady, MemoryPressure, DiskPressure, PIDPressure, Unschedulable); пустой список — проблем нет"`
	CPUCores  float64       `json:"cpu_cores_allocatable"`
	MemoryGiB float64       `json:"memory_gib_allocatable"`
}

type NodeProblem struct {
	Name     string   `json:"name"`
	Problems []string `json:"problems"`
}

type ClusterPods struct {
	Total         int                 `json:"total"`
	Running       int                 `json:"running"`
	Pending       int                 `json:"pending"`
	Failed        int                 `json:"failed"`
	Problems      []ClusterPodProblem `json:"problems" jsonschema:"пустой список — проблемных подов нет"`
	ProblemsTotal int                 `json:"problems_total"`
	Truncated     bool                `json:"truncated"`
}

type ClusterPodProblem struct {
	Namespace string    `json:"namespace"`
	Pod       string    `json:"pod"`
	Service   string    `json:"service,omitempty"`
	Image     string    `json:"image,omitempty"`
	Reason    string    `json:"reason"`
	Message   string    `json:"message,omitempty"`
	Since     time.Time `json:"since,omitzero"`
}

type EventReason struct {
	Reason     string    `json:"reason"`
	Count      int       `json:"count"`
	Namespaces int       `json:"namespaces"`
	Services   []string  `json:"services,omitempty" jsonschema:"сервисы каталога, к объектам которых относятся события"`
	Example    string    `json:"example"`
	LastTS     time.Time `json:"last_ts"`
}

func EncodeClusterHealthRep(v *clusterModel.Health) ClusterHealthRep {
	return ClusterHealthRep{
		GeneratedAt:  tz.In(v.GeneratedAt),
		Window:       window.Format(v.Window),
		Health:       v.Health,
		SummaryHints: lo.Ternary(v.SummaryHints == nil, []string{}, v.SummaryHints),
		Nodes: ClusterNodes{
			Total: v.Nodes.Total, Ready: v.Nodes.Ready,
			Problems: lo.Map(v.Nodes.Problems, func(n clusterModel.NodeProblem, _ int) NodeProblem {
				return NodeProblem{Name: n.Name, Problems: n.Problems}
			}),
			CPUCores:  float64(v.Nodes.CPUMillis) / 1000,
			MemoryGiB: float64(v.Nodes.MemoryBytes) / (1 << 30),
		},
		Pods: ClusterPods{
			Total: v.Pods.Total, Running: v.Pods.Running, Pending: v.Pods.Pending, Failed: v.Pods.Failed,
			Problems: lo.Map(v.Pods.Problems, func(p clusterModel.PodProblem, _ int) ClusterPodProblem {
				return ClusterPodProblem{Namespace: p.Namespace, Pod: p.Pod, Service: p.Service, Image: p.Image, Reason: p.Reason, Message: p.Message, Since: tz.In(p.Since)}
			}),
			ProblemsTotal: v.Pods.ProblemsTotal,
			Truncated:     v.Pods.ProblemsTotal > len(v.Pods.Problems),
		},
		EventReasons: lo.Map(v.EventReasons, func(r clusterModel.EventReason, _ int) EventReason {
			return EventReason{Reason: r.Reason, Count: r.Count, Namespaces: r.Namespaces, Services: r.Services, Example: r.Example, LastTS: tz.In(r.LastTS)}
		}),
		InfraAlerts:         lo.Map(v.InfraAlerts, encodeAlert),
		ServiceAlertsActive: v.ServiceAlertsActive,
		Metrics:             lo.Map(v.Metrics, encodeMetric),
		LogErrors:           encodeLogErrors(v.LogErrors),
		Errors:              lo.Map(v.Errors, encodeSnapshotSourceError),
	}
}

// topErrorChars — шаблон ошибки в выжимке по кластеру короче, чем в query_logs: ответ
// get_cluster_health не должен расти от логов.
const topErrorChars = 300

func encodeLogErrors(v *logsModel.ClusterErrors) *LogErrors {
	if v == nil {
		return nil
	}
	return &LogErrors{
		Window:        window.Format(v.Window),
		Total:         v.Total,
		ServicesTotal: v.ServicesTotal,
		Services: lo.Map(v.Services, func(s logsModel.ServiceErrors, _ int) ServiceLogError {
			e := ServiceLogError{Service: s.Service, Namespace: s.Namespace, Count: s.Count}
			if s.Top.Count > 0 {
				e.TopError, e.LastSeen = lo.Ellipsis(s.Top.Template, topErrorChars), tz.In(s.Top.LastSeen)
			}
			return e
		}),
	}
}
