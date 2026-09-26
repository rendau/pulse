package dto

import (
	"time"

	"github.com/samber/lo"

	eventModel "github.com/mechta-market/pulse/internal/domain/event/model"
	snapshotModel "github.com/mechta-market/pulse/internal/domain/snapshot/model"
	usecaseSnapshotModel "github.com/mechta-market/pulse/internal/usecase/snapshot/model"
	"github.com/mechta-market/pulse/internal/util/tz"
	"github.com/mechta-market/pulse/internal/util/window"
)

// get_service_snapshot

type GetServiceSnapshotReq struct {
	Service string `json:"service" jsonschema:"точное имя сервиса из resolve_service или list_services"`
	Window  string `json:"window,omitempty" jsonschema:"окно для событий и алертов, Go duration: 15m, 1h (по умолчанию), 24h, 7d (максимум)"`
}

type SnapshotRep struct {
	Service      string          `json:"service"`
	GeneratedAt  time.Time       `json:"generated_at"`
	Window       string          `json:"window"`
	Health       string          `json:"health" jsonschema:"healthy | degraded | down | unknown — вычислено по фиксированным правилам"`
	SummaryHints []string        `json:"summary_hints" jsonschema:"факты, а не выводы; выводы делает модель"`
	Alerts       []Alert         `json:"alerts"`
	Workloads    []WorkloadState `json:"workloads"`
	Pods         PodsSummary     `json:"pods"`
	Metrics      []Metric        `json:"metrics"`
	TopErrors    []LogPattern    `json:"top_errors" jsonschema:"верхние error-паттерны логов за окно; подробнее — query_logs"`
	Self         *SelfReport     `json:"self_reported,omitempty" jsonschema:"что сервис сообщает о себе сам (ручка состояния манифеста): зависимости и показатели; причина часто здесь"`
	RecentEvents []Event         `json:"recent_events"`
	Errors       []SourceError   `json:"errors" jsonschema:"источники, которые не ответили: часть картины отсутствует"`
}

type Alert struct {
	Name     string            `json:"name"`
	Severity string            `json:"severity,omitempty"`
	State    string            `json:"state" jsonschema:"active | suppressed"`
	StartsAt time.Time         `json:"starts_at"`
	Summary  string            `json:"summary,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
	Count    int               `json:"count,omitempty" jsonschema:"сколько алертов с этим именем слито в один; различающиеся лейблы перечислены через запятую"`
}

type WorkloadState struct {
	Namespace       string     `json:"namespace"`
	Kind            string     `json:"kind"`
	Name            string     `json:"name"`
	ReplicasDesired int32      `json:"replicas_desired"`
	Image           string     `json:"image"`
	DeployedCommit  string     `json:"deployed_commit,omitempty"`
	Pods            *PodsState `json:"pods,omitempty"`
}

type PodsSummary struct {
	Ready    int          `json:"ready"`
	Total    int          `json:"total"`
	Restarts int32        `json:"restarts"`
	Problems []PodProblem `json:"problems,omitempty"`
}

type PodProblem struct {
	Pod       string    `json:"pod"`
	Container string    `json:"container,omitempty"`
	Reason    string    `json:"reason"`
	Message   string    `json:"message,omitempty"`
	At        time.Time `json:"at,omitempty"`
}

type Metric struct {
	Id                  string   `json:"id"`
	Title               string   `json:"title,omitempty"`
	Unit                string   `json:"unit,omitempty"`
	Direction           string   `json:"direction,omitempty"`
	Current             *float64 `json:"current"`
	HourAgo             *float64 `json:"hour_ago"`
	SameTimeYesterday   *float64 `json:"same_time_yesterday"`
	DeltaVsHourAgoPct   *float64 `json:"delta_vs_hour_ago_pct"`
	DeltaVsYesterdayPct *float64 `json:"delta_vs_yesterday_pct"`
	Anomaly             bool     `json:"anomaly"`
	Error               string   `json:"error,omitempty"`
}

type Event struct {
	TS       time.Time      `json:"ts"`
	Source   string         `json:"source"`
	Type     string         `json:"type"`
	Service  string         `json:"service"`
	Severity string         `json:"severity"`
	Summary  string         `json:"summary"`
	Details  map[string]any `json:"details,omitempty"`
}

func EncodeSnapshotRep(v *snapshotModel.Snapshot) SnapshotRep {
	rep := SnapshotRep{
		Service:      v.Service,
		GeneratedAt:  tz.In(v.GeneratedAt),
		Window:       window.Format(v.Window),
		Health:       v.Health,
		SummaryHints: lo.Ternary(v.SummaryHints == nil, []string{}, v.SummaryHints),
		Alerts:       lo.Map(v.Alerts, encodeAlert),
		Workloads:    lo.Map(v.Workloads, encodeWorkloadState),
		Metrics:      lo.Map(v.Metrics, encodeMetric),
		TopErrors:    lo.Map(v.TopErrors, EncodeLogPattern),
		RecentEvents: lo.Map(v.RecentEvents, EncodeEvent),
		Errors:       lo.Map(v.Errors, encodeSnapshotSourceError),
		Self:         encodeSelfReport(v.Self),
	}

	for _, w := range v.Workloads {
		rep.Pods.Ready += w.Pods.Ready
		rep.Pods.Total += w.Pods.Total
		rep.Pods.Restarts += w.Pods.Restarts
		rep.Pods.Problems = append(rep.Pods.Problems, lo.Map(w.Pods.Problems, encodePodProblem)...)
	}

	return rep
}

func encodeAlert(v snapshotModel.Alert, _ int) Alert {
	return Alert{Name: v.Name, Severity: v.Severity, State: v.State, StartsAt: tz.In(v.StartsAt), Summary: v.Summary, Labels: v.Labels, Count: v.Count}
}

func encodeWorkloadState(v snapshotModel.WorkloadState, _ int) WorkloadState {
	result := WorkloadState{
		Namespace: v.Namespace, Kind: v.Kind, Name: v.Name,
		ReplicasDesired: v.ReplicasDesired, Image: v.Image, DeployedCommit: v.DeployedCommit,
	}
	if v.Pods.Total > 0 || len(v.Pods.Problems) > 0 {
		result.Pods = &PodsState{
			Ready: v.Pods.Ready, Total: v.Pods.Total, Restarts: v.Pods.Restarts,
			Problems: lo.Map(v.Pods.Problems, func(p snapshotModel.PodProblem, _ int) string {
				return lo.CoalesceOrEmpty(p.Container, p.Pod) + ": " + p.Reason
			}),
		}
	}
	return result
}

func encodePodProblem(v snapshotModel.PodProblem, _ int) PodProblem {
	return PodProblem{Pod: v.Pod, Container: v.Container, Reason: v.Reason, Message: v.Message, At: tz.In(v.At)}
}

func encodeMetric(v snapshotModel.Metric, _ int) Metric {
	return Metric{
		Id: v.Id, Title: v.Title, Unit: v.Unit, Direction: v.Direction,
		Current: v.Current, HourAgo: v.HourAgo, SameTimeYesterday: v.SameTimeYesterday,
		DeltaVsHourAgoPct: v.DeltaVsHourAgo, DeltaVsYesterdayPct: v.DeltaVsYesterday,
		Anomaly: v.Anomaly, Error: v.Error,
	}
}

func EncodeEvent(v eventModel.Event, _ int) Event {
	return Event{TS: tz.In(v.TS), Source: v.Source, Type: v.Type, Service: v.Service, Severity: v.Severity, Summary: v.Summary, Details: v.Details}
}

func encodeSnapshotSourceError(v snapshotModel.SourceError, _ int) SourceError {
	return SourceError{Source: v.Source, Message: v.Message}
}

// query_metrics

type QueryMetricsReq struct {
	Service  string `json:"service" jsonschema:"точное имя сервиса"`
	MetricId string `json:"metric_id,omitempty" jsonschema:"id метрики из get_service_info/get_service_snapshot; приоритетнее promql"`
	PromQL   string `json:"promql,omitempty" jsonschema:"произвольный PromQL, fallback; лимиты: 20 серий, 200 точек на серию"`
	Window   string `json:"window,omitempty" jsonschema:"Go duration: 15m, 1h (по умолчанию), 24h, 7d (максимум)"`
	Step     string `json:"step,omitempty" jsonschema:"шаг ряда, Go duration; по умолчанию window/100, минимум 15s"`
}

type QueryMetricsRep struct {
	Service  string    `json:"service"`
	MetricId string    `json:"metric_id"`
	Title    string    `json:"title,omitempty"`
	Unit     string    `json:"unit,omitempty"`
	PromQL   string    `json:"promql"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Step     string    `json:"step"`
	Series   []Series  `json:"series"`
}

type Series struct {
	Labels map[string]string `json:"labels,omitempty"`
	Points []Point           `json:"points"`
}

type Point struct {
	TS    time.Time `json:"ts"`
	Value float64   `json:"value"`
}

func EncodeQueryMetricsRep(v *usecaseSnapshotModel.QueryMetricsResult) QueryMetricsRep {
	return QueryMetricsRep{
		Service:  v.Service,
		MetricId: v.Def.Id,
		Title:    v.Def.Title,
		Unit:     v.Def.Unit,
		PromQL:   v.Def.PromQL,
		Start:    tz.In(v.Start),
		End:      tz.In(v.End),
		Step:     window.Format(v.Step),
		Series: lo.Map(v.Series, func(s snapshotModel.Series, _ int) Series {
			return Series{
				Labels: s.Labels,
				Points: lo.Map(s.Points, func(p snapshotModel.Point, _ int) Point { return Point{TS: tz.In(p.TS), Value: p.Value} }),
			}
		}),
	}
}

type SelfReport struct {
	Status       string           `json:"status" jsonschema:"ok | degraded | down — по словам сервиса"`
	Pod          string           `json:"pod" jsonschema:"под, чей отчёт показан (худший из опрошенных)"`
	Pods         int              `json:"pods" jsonschema:"сколько подов ответило"`
	CheckedAt    *time.Time       `json:"checked_at,omitempty" jsonschema:"когда сервис выполнял проверки"`
	Stale        bool             `json:"stale,omitempty" jsonschema:"проверки давно не выполнялись — отчёту верить с осторожностью"`
	Dependencies []SelfDependency `json:"dependencies,omitempty"`
	Gauges       []SelfGauge      `json:"gauges,omitempty"`
}

type SelfDependency struct {
	Id        string `json:"id"`
	Kind      string `json:"kind"`
	Target    string `json:"target"`
	Critical  bool   `json:"critical,omitempty"`
	Affects   string `json:"affects,omitempty" jsonschema:"что ломается, когда она недоступна (со слов владельца)"`
	Status    string `json:"status,omitempty" jsonschema:"пусто — сервис не прислал состояние этой зависимости"`
	LatencyMs *int64 `json:"latency_ms,omitempty"`
	Message   string `json:"message,omitempty"`
}

type SelfGauge struct {
	Id     string     `json:"id"`
	Title  string     `json:"title"`
	Value  *float64   `json:"value,omitempty"`
	Time   *time.Time `json:"time,omitempty"`
	Unit   string     `json:"unit,omitempty"`
	Status string     `json:"status,omitempty"`
}

func encodeSelfReport(v *snapshotModel.SelfReport) *SelfReport {
	if v == nil {
		return nil
	}
	rep := &SelfReport{
		Status: v.Status, Pod: v.Pod, Pods: v.Pods, Stale: v.Stale,
		Dependencies: lo.Map(v.Dependencies, func(d snapshotModel.SelfDependency, _ int) SelfDependency {
			return SelfDependency{Id: d.Id, Kind: d.Kind, Target: d.Target, Critical: d.Critical, Affects: d.Affects, Status: d.Status, LatencyMs: d.LatencyMs, Message: d.Message}
		}),
		Gauges: lo.Map(v.Gauges, func(g snapshotModel.SelfGauge, _ int) SelfGauge {
			gauge := SelfGauge{Id: g.Id, Title: g.Title, Value: g.Value, Unit: g.Unit, Status: g.Status}
			if g.Time != nil {
				gauge.Time = new(tz.In(*g.Time))
			}
			return gauge
		}),
	}
	if !v.CheckedAt.IsZero() {
		rep.CheckedAt = new(tz.In(v.CheckedAt))
	}
	return rep
}
