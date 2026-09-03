package model

import (
	"time"

	eventModel "github.com/mechta-market/pulse/internal/domain/event/model"
	logsModel "github.com/mechta-market/pulse/internal/domain/logs/model"
)

// Health — детерминированная оценка состояния сервиса.
const (
	HealthHealthy  = "healthy"
	HealthDegraded = "degraded"
	HealthDown     = "down"
	HealthUnknown  = "unknown"
)

// Alert — активный алерт Alertmanager, относящийся к сервису.
type Alert struct {
	Name        string
	Severity    string
	State       string // active | suppressed
	StartsAt    time.Time
	Summary     string
	Labels      map[string]string
	Annotations map[string]string
}

// WorkloadState — workload и живое состояние его подов.
type WorkloadState struct {
	Namespace       string
	Kind            string
	Name            string
	ReplicasDesired int32
	Image           string
	DeployedCommit  string
	Pods            PodsState
}

// PodsState — сводка по подам workload'а.
type PodsState struct {
	Ready    int
	Total    int
	Restarts int32
	// NewestStartedAt — старт самого свежего пода: даёт «выкатка N минут назад»
	NewestStartedAt time.Time
	Problems        []PodProblem
}

// PodProblem — проблема конкретного пода/контейнера в человекочитаемом виде.
type PodProblem struct {
	Pod       string
	Container string
	Reason    string // CrashLoopBackOff | ImagePullBackOff | OOMKilled | Pending | …
	Message   string
	At        time.Time
}

// MetricDef — что запрашивать: из service.yaml или из дефолтного набора.
type MetricDef struct {
	Id        string
	Title     string
	PromQL    string
	Unit      string
	Direction string // higher_is_better | lower_is_better | ""
}

// Metric — метрика в сравнении с базовой линией (Р6).
type Metric struct {
	MetricDef

	Current           *float64
	HourAgo           *float64
	SameTimeYesterday *float64
	DeltaVsYesterday  *float64 // проценты
	DeltaVsHourAgo    *float64 // проценты
	Anomaly           bool
	Error             string
}

// SourceError — источник не ответил; часть картины отсутствует.
type SourceError struct {
	Source  string
	Message string
}

// Snapshot — агрегированный срез состояния сервиса.
type Snapshot struct {
	Service      string
	GeneratedAt  time.Time
	Window       time.Duration
	Health       string
	SummaryHints []string
	Alerts       []Alert
	Workloads    []WorkloadState
	Metrics      []Metric
	// TopErrors — верхние error-паттерны логов за окно (фаза 3)
	TopErrors    []logsModel.Pattern
	RecentEvents []eventModel.Event
	Errors       []SourceError
}

// Series — ряд значений для query_metrics.
type Series struct {
	Labels map[string]string
	Points []Point
}

type Point struct {
	TS    time.Time
	Value float64
}
