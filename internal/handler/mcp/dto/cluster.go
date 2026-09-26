package dto

import (
	"math"
	"time"

	"github.com/samber/lo"

	clusterModel "github.com/rendau/pulse/internal/domain/cluster/model"
	logsModel "github.com/rendau/pulse/internal/domain/logs/model"
	"github.com/rendau/pulse/internal/util/tz"
	"github.com/rendau/pulse/internal/util/window"
)

type GetClusterHealthReq struct {
	Window string `json:"window,omitempty" jsonschema:"окно для событий и OOM: 15m, 1h (по умолчанию), 24h"`
}

type ClusterHealthRep struct {
	GeneratedAt         time.Time          `json:"generated_at"`
	Window              string             `json:"window"`
	Health              string             `json:"health" jsonschema:"healthy | degraded | down | unknown — по фиксированным правилам"`
	SummaryHints        []string           `json:"summary_hints"`
	Nodes               ClusterNodes       `json:"nodes"`
	Pods                ClusterPods        `json:"pods"`
	EventReasons        []EventReason      `json:"warning_events_by_reason"`
	InfraAlerts         []Alert            `json:"infra_alerts" jsonschema:"активные алерты, не привязанные к сервисам каталога"`
	ServiceAlertsActive int                `json:"service_alerts_active"`
	Metrics             []Metric           `json:"metrics"`
	LogErrors           *LogErrors         `json:"log_errors,omitempty" jsonschema:"ошибки в логах всего кластера по сервисам; нет — логи недоступны (см. errors)"`
	SelfReported        []ClusterSelf      `json:"self_reported" jsonschema:"сервисы с манифестом, которые сами сообщают о проблеме (самоотчёт не ok или устарел); подробности — self_reported в get_service_snapshot"`
	PublicApps          []ClusterPublicApp `json:"public_apps" jsonschema:"приложения API-gateway (опубликованный наружу API) с проблемой за окно; пустой список — проблем нет или gateway не подключён; подробности — get_public_api(service)"`
	Errors              []SourceError      `json:"errors"`
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
		SelfReported: lo.Map(v.SelfReported, func(s clusterModel.ServiceSelfReport, _ int) ClusterSelf {
			rep := ClusterSelf{Service: s.Service, Status: s.Report.Status, Stale: s.Report.Stale, Pod: s.Report.Pod, Hints: lo.Ternary(s.Hints == nil, []string{}, s.Hints)}
			if !s.Report.CheckedAt.IsZero() {
				rep.CheckedAt = new(tz.In(s.Report.CheckedAt))
			}
			return rep
		}),
		PublicApps: lo.Map(v.PublicApps, encodePublicApp),
		Errors:     lo.Map(v.Errors, encodeSnapshotSourceError),
	}
}

// ClusterPublicApp — приложение gateway с проблемой снаружи.
type ClusterPublicApp struct {
	App           string                  `json:"app" jsonschema:"приложение gateway"`
	Service       string                  `json:"service,omitempty" jsonschema:"сервис-бэкенд каталога; пусто — не найден"`
	Problems      []ClusterPublicProblem  `json:"problems"`
	Traffic       *ClusterPublicTraffic   `json:"traffic,omitempty" jsonschema:"трафик за окно по метрикам gateway и обычный уровень"`
	BackendErrors []ClusterGatewayReason  `json:"backend_errors,omitempty" jsonschema:"backend не ответил gateway (логи gateway), по причинам"`
	ScriptErrors  []ClusterScriptError    `json:"script_errors,omitempty" jsonschema:"ошибки скриптов трансформации маршрутов (логи gateway)"`
	BackendPods   *ClusterPublicPodsReady `json:"backend_pods,omitempty" jsonschema:"готовые поды backend'а против желаемых"`
}

type ClusterPublicProblem struct {
	Kind string `json:"kind" jsonschema:"errors — всплеск 5xx, backend — backend не отвечает или без готовых подов, script — сломан скрипт маршрута, slow — p95 выше обычного, no_traffic — пропал трафик"`
	Text string `json:"text"`
}

type ClusterPublicTraffic struct {
	Requests          float64  `json:"requests"`
	Errors            float64  `json:"errors" jsonschema:"ответы 5xx и серверные коды gRPC"`
	ErrorRate         *float64 `json:"error_rate,omitempty"`
	UsualErrorRate    *float64 `json:"usual_error_rate,omitempty" jsonschema:"доля сбоев за сутки до окна"`
	P95               *float64 `json:"p95_seconds,omitempty"`
	UsualP95          *float64 `json:"usual_p95_seconds,omitempty" jsonschema:"то же окно вчера (нет — окном раньше)"`
	PrevRequests      *float64 `json:"prev_requests,omitempty" jsonschema:"запросов окном раньше"`
	YesterdayRequests *float64 `json:"yesterday_requests,omitempty" jsonschema:"запросов в то же окно вчера"`
}

type ClusterGatewayReason struct {
	Reason  string `json:"reason"`
	Count   int    `json:"count"`
	Example string `json:"example,omitempty"`
}

type ClusterScriptError struct {
	Route   string `json:"route,omitempty" jsonschema:"маршрут (METHOD /prefix/path); пусто — маршрута уже нет в конфигурации"`
	Reason  string `json:"reason" jsonschema:"request/response transform: compile/run failed"`
	Count   int    `json:"count"`
	Example string `json:"example,omitempty"`
}

type ClusterPublicPodsReady struct {
	Ready   int `json:"ready"`
	Desired int `json:"desired"`
}

func encodePublicApp(v clusterModel.PublicApp, _ int) ClusterPublicApp {
	rep := ClusterPublicApp{
		App: v.App, Service: v.Service,
		Problems: lo.Map(v.Problems, func(p clusterModel.PublicProblem, _ int) ClusterPublicProblem {
			return ClusterPublicProblem{Kind: p.Kind, Text: p.Text}
		}),
		BackendErrors: lo.Map(v.BackendErrors, func(r clusterModel.GatewayReason, _ int) ClusterGatewayReason {
			return ClusterGatewayReason{Reason: r.Reason, Count: r.Count, Example: r.Example}
		}),
		ScriptErrors: lo.Map(v.ScriptErrors, func(e clusterModel.ScriptError, _ int) ClusterScriptError {
			return ClusterScriptError{Route: e.Route, Reason: e.Reason, Count: e.Count, Example: e.Example}
		}),
	}
	if t := v.Traffic; t != nil {
		rep.Traffic = &ClusterPublicTraffic{
			Requests: roundTo(t.Requests), Errors: roundTo(t.Errors), ErrorRate: roundPtr(t.ErrorRate), UsualErrorRate: roundPtr(t.UsualErrorRate),
			P95: roundPtr(t.P95), UsualP95: roundPtr(t.UsualP95), PrevRequests: roundPtr(t.PrevRequests), YesterdayRequests: roundPtr(t.YesterdayRequests),
		}
	}
	if p := v.BackendPods; p != nil {
		rep.BackendPods = &ClusterPublicPodsReady{Ready: p.Ready, Desired: p.Desired}
	}
	return rep
}

// roundTo — три знака после запятой: increase() даёт дробные счётчики.
func roundTo(v float64) float64 {
	return math.Round(v*1000) / 1000
}

func roundPtr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	return new(roundTo(*v))
}

// ClusterSelf — сервис, который сам сообщает о проблеме.
type ClusterSelf struct {
	Service   string     `json:"service"`
	Status    string     `json:"status" jsonschema:"ok | degraded | down — худший под"`
	Stale     bool       `json:"stale,omitempty" jsonschema:"отчёт устарел: фоновая проверка в сервисе остановилась"`
	Pod       string     `json:"pod"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	Hints     []string   `json:"hints" jsonschema:"что именно: зависимости, показатели, застрявшие объекты"`
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
