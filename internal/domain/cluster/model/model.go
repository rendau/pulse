package model

import (
	"time"

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
	Errors              []snapshotModel.SourceError
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
	Service   string // сервис каталога, если под относится к его workload'у
	Reason    string
	Message   string
	Since     time.Time
}

// EventReason — Warning-события кластера за окно, сгруппированные по причине.
type EventReason struct {
	Reason     string
	Count      int
	Namespaces int
	Example    string
	LastTS     time.Time
}
