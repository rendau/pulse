package model

import (
	"time"

	logsModel "github.com/rendau/pulse/internal/domain/logs/model"
	snapshotModel "github.com/rendau/pulse/internal/domain/snapshot/model"
)

// Health — состояние кластера в целом.
type Health struct {
	GeneratedAt  time.Time
	Window       time.Duration
	Health       string // healthy | degraded | down | unknown
	SummaryHints []string
	Nodes        Nodes
	Pods         Pods
	EventReasons []EventReason
	// InfraAlerts — активные алерты, не относящиеся ни к одному сервису каталога
	InfraAlerts         []snapshotModel.Alert
	ServiceAlertsActive int
	Metrics             []snapshotModel.Metric
	// LogErrors — ошибки в логах всего кластера по сервисам; nil — логи недоступны
	LogErrors *logsModel.ClusterErrors
	// SelfReported — сервисы, которые сами сообщают о проблеме (самоотчёт не ok или устарел)
	SelfReported []ServiceSelfReport
	// PublicApps — приложения gateway (опубликованный наружу API) с проблемой: сбои, backend не
	// отвечает, сломан скрипт маршрута, медленно, пропал трафик
	PublicApps []PublicApp
	Errors     []snapshotModel.SourceError
}

// ServiceSelfReport — самоотчёт сервиса не в норме и подсказки по нему («сервис сообщает: …»).
type ServiceSelfReport struct {
	Service string
	Report  *snapshotModel.SelfReport
	Hints   []string
}

type Nodes struct {
	Total    int
	Ready    int
	Problems []NodeProblem
	// CPUMillis, MemoryBytes — суммарные allocatable
	CPUMillis   int64
	MemoryBytes int64
}

type NodeProblem struct {
	Name     string
	Problems []string // NotReady, MemoryPressure, DiskPressure, Unschedulable…
}

type Pods struct {
	Total     int
	Running   int
	Pending   int
	Failed    int
	Succeeded int
	Problems  []PodProblem
	// ProblemsTotal — до усечения
	ProblemsTotal int
}

type PodProblem struct {
	Namespace string
	Pod       string
	Service   string // сервис каталога: по workload'у, образу или оркестратору (managed-by)
	Image     string // образ проблемного контейнера без тега и digest
	Reason    string
	Message   string
	Since     time.Time
}

// EventReason — Warning-события кластера за окно, сгруппированные по причине.
type EventReason struct {
	Reason     string
	Count      int
	Namespaces int
	Services   []string // сервисы каталога, к объектам которых относятся события
	Example    string
	LastTS     time.Time
}

// виды проблем публичного приложения
const (
	PublicErrors    = "errors"     // всплеск 5xx против обычного уровня
	PublicBackend   = "backend"    // backend не отвечает gateway или у него нет готовых подов
	PublicScript    = "script"     // скрипт трансформации маршрута не компилируется или падает
	PublicSlow      = "slow"       // p95 заметно выше обычного
	PublicNoTraffic = "no_traffic" // обычно есть запросы, а сейчас почти нет
)

// PublicApp — приложение gateway и что с ним не так снаружи.
type PublicApp struct {
	App string
	// Service — сервис-бэкенд каталога; пусто — не найден
	Service  string
	Problems []PublicProblem
	Traffic  *PublicTraffic
	// BackendErrors — backend не ответил gateway (логи gateway), по причинам
	BackendErrors []GatewayReason
	// ScriptErrors — ошибки скриптов трансформации по маршрутам (логи gateway)
	ScriptErrors []ScriptError
	// BackendPods — готовые поды backend'а против желаемых; nil — неизвестно
	BackendPods *PodsReady
}

// PublicProblem — проблема приложения: вид и суть с цифрами.
type PublicProblem struct {
	Kind string
	Text string
}

// PublicTraffic — трафик приложения за окно по метрикам gateway и обычный уровень.
type PublicTraffic struct {
	Requests float64
	// Errors — ответы 5xx и серверные коды gRPC
	Errors    float64
	ErrorRate *float64
	// UsualErrorRate — доля сбоев за сутки до окна
	UsualErrorRate *float64
	// P95 — сейчас; UsualP95 — то же окно вчера (нет — окном раньше), секунды
	P95      *float64
	UsualP95 *float64
	// PrevRequests — окном раньше; YesterdayRequests — то же окно вчера
	PrevRequests      *float64
	YesterdayRequests *float64
}

// GatewayReason — ошибки backend'а по одной причине («backend connection refused»).
type GatewayReason struct {
	Reason  string
	Count   int
	Example string
}

// ScriptError — ошибки скрипта трансформации одного маршрута.
type ScriptError struct {
	Route   string
	Reason  string
	Count   int
	Example string
}

type PodsReady struct {
	Ready   int
	Desired int
}
