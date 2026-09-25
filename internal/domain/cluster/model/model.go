package model

import (
	"time"

	logsModel "github.com/mechta-market/pulse/internal/domain/logs/model"
	snapshotModel "github.com/mechta-market/pulse/internal/domain/snapshot/model"
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
	Errors    []snapshotModel.SourceError
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
