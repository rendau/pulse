package model

import "time"

// Event — нормализованное событие: единый тип для всего, что имеет время.
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

// PodTemplateRevision — ревизия шаблона пода Deployment'а (ReplicaSet) с аннотациями шаблона.
type PodTemplateRevision struct {
	Name        string
	Revision    int64
	CreatedAt   time.Time
	Annotations map[string]string
}

// причины выкатки, выводимые из аннотаций шаблона
const (
	// RolloutCauseConfigReload — reloader перекатил поды после смены configmap/secret
	RolloutCauseConfigReload = "config_reload"
	// RolloutCauseRestart — ручной kubectl rollout restart
	RolloutCauseRestart = "restart"
)

// Rollout — выкатка workload'а с известной причиной.
type Rollout struct {
	TS       time.Time
	Workload string // namespace/name
	Revision int64
	Cause    string // config_reload | restart
	// ConfigKind / ConfigName / Hash / PrevHash — для config_reload: объект конфигурации и
	// отпечатки его содержимого (reloader), не значения
	ConfigKind string // configmap | secret
	ConfigName string
	Hash       string
	PrevHash   string
	// Sync — запуск sync kusec, применивший изменение (если найден): автор и изменённые ключи
	Sync *ConfigSync
}

// ConfigEdit — изменение в kusec (запись аудита) в доменном виде; значения уже маскированы.
type ConfigEdit struct {
	TS         time.Time
	Author     string
	Origin     string // ui | api | mcp | system
	Action     string // create | update | delete | activate | deactivate | import
	ObjectKind string // configmap | secret | app
	ObjectName string // kusec-caravan-main
	Key        string // пусто — изменение самого объекта
	// ValueChanged — изменилось значение ключа (для секрета — только факт)
	ValueChanged bool
	OldValue     string
	NewValue     string
	// Fields — прочие изменённые поля (description, active, value_format…)
	Fields []string
}

// ConfigSync — применение конфигурации kusec в кластер.
type ConfigSync struct {
	RunId   string
	TS      time.Time
	Author  string
	Status  string // ok | partial | error | running
	Error   string
	Objects []ConfigSyncObject
}

// ConfigSyncObject — k8s-объект, изменённый запуском sync.
type ConfigSyncObject struct {
	ObjectKind  string // configmap | secret
	ObjectName  string
	Op          string // created | updated | deleted | error
	ChangedKeys []string
}
