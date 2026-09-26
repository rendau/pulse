package model

import (
	"strings"
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
	// Count — число слитых в один алертов с тем же именем (0 — не сливался)
	Count int
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

// Hint — подсказка об ошибке источника: не успел ответить за дедлайн или недоступен.
func (e SourceError) Hint() string {
	if strings.Contains(e.Message, "deadline exceeded") {
		return "источник " + e.Source + " не успел ответить: часть картины отсутствует"
	}
	return "источник " + e.Source + " недоступен: часть картины отсутствует"
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
	TopErrors []logsModel.Pattern
	// Self — что сервис сообщает о себе сам (ручка состояния манифеста); nil — ручки нет
	Self         *SelfReport
	RecentEvents []eventModel.Event
	Errors       []SourceError
}

// SelfReport — состояние, которое сервис сообщает сам: зависимости и показатели. Худший из
// опрошенных подов.
type SelfReport struct {
	Status    string // ok | degraded | down
	Pod       string // под, чей отчёт показан
	Pods      int    // сколько подов ответило
	CheckedAt time.Time
	// Stale — проверки давно не выполнялись (фоновая проверка в сервисе остановилась)
	Stale        bool
	Dependencies []SelfDependency
	Gauges       []SelfGauge
	// Entities — бизнес-объекты из domain манифеста со счётчиками от сервиса
	Entities []SelfEntity
}

// SelfEntity — объект: сколько в каждом статусе и сколько застряло (порог — из domain).
type SelfEntity struct {
	Name       string
	Status     string
	Statuses   []SelfEntityStatus
	Created1h  *int64
	Finished1h *int64
}

type SelfEntityStatus struct {
	Name       string
	Meaning    string        // из domain манифеста
	StuckAfter time.Duration // из domain манифеста; 0 — не задано
	Count      int64
	Stuck      int64
	Oldest     time.Duration
}

// SelfDependency — зависимость из манифеста и её состояние по словам сервиса.
type SelfDependency struct {
	Id        string
	Kind      string
	Target    string
	Critical  bool
	Affects   string // что ломается, когда она недоступна (со слов владельца)
	Status    string
	LatencyMs *int64
	Message   string
}

type SelfGauge struct {
	Id     string
	Title  string
	Value  *float64
	Time   *time.Time
	Unit   string
	Status string
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
