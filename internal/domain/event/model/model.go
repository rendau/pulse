package model

import "time"

// Event — нормализованное событие (ТЗ 1.2): единый тип для всего, что имеет время.
// Summary пишется так, чтобы показать человеку без обработки.
type Event struct {
	TS       time.Time
	Source   string // k8s | prometheus | github | kusec | alertmanager | ruto
	Type     string // deploy | restart | oom_kill | alert_firing | config_change | commit | scale | warning | info
	Service  string
	Severity string // info | warning | critical
	Summary  string
	Details  map[string]any
}

// ClusterEvent — событие кластера в доменном виде (вход для нормализации).
type ClusterEvent struct {
	TS         time.Time
	ObjectKind string
	ObjectName string
	Reason     string
	Type       string // Normal | Warning
	Message    string
	Count      int32
}

// ContainerTermination — последнее завершение контейнера из статуса пода.
type ContainerTermination struct {
	At        time.Time
	Pod       string
	Container string
	Reason    string // OOMKilled | Error | Completed …
	Restarts  int32
}
