package model

import "time"

// виды k8s-объектов kusec
const (
	KubeKindSecret    = "Secret"
	KubeKindConfigMap = "ConfigMap"
)

// сущности аудита
const (
	EntityApp        = "app"
	EntitySecret     = "secret"
	EntityItem       = "item"
	EntityConfigMap  = "configmap"
	EntityConfigItem = "config_item"
)

// статусы и операции sync
const (
	SyncOpUnchanged = "unchanged"
	SyncStatusOk    = "ok"
)

// ValueField — поле значения ключа в changes аудита
const ValueField = "value"

// Resolved — k8s-объект, описанный в kusec.
type Resolved struct {
	Found     bool
	AppId     string
	AppSlug   string
	Namespace string
	KubeKind  string // Secret | ConfigMap
	ObjectId  string
}

type AuditReq struct {
	AppId string
	Since time.Time
	Until time.Time
	Limit int
}

// AuditEntry — одно изменение в kusec.
type AuditEntry struct {
	Id         int64
	CreatedAt  time.Time
	ActorName  string
	Source     string // ui | api | mcp | system
	EntityType string // app | secret | item | configmap | config_item | api_key | usr | sync_run
	EntityId   string
	AppId      string
	Namespace  string
	AppSlug    string
	KubeKind   string
	KubeName   string
	Key        string
	Action     string // create | update | delete | activate | deactivate | sync | import
	BatchId    string
	Changes    []AuditChange
}

// AuditChange — изменение поля. Для значения секрета Old/New пустые, есть только отпечатки
// и размеры; для значения обычного конфига длиннее 4 КБ — отпечаток и Truncated.
type AuditChange struct {
	Field     string
	Old       *string
	New       *string
	OldHash   string
	NewHash   string
	OldSize   *int64
	NewSize   *int64
	Truncated bool
}

type SyncRunReq struct {
	AppId string
	Since time.Time
	Until time.Time
	Limit int
}

// SyncRun — применение конфигурации в кластер.
type SyncRun struct {
	Id         string
	StartedAt  time.Time
	FinishedAt *time.Time
	Status     string // running | ok | partial | error
	Error      string
	ActorName  string
	Source     string
	AppId      string
	DurationMs int64
	Objects    []SyncObject
}

type SyncObject struct {
	Namespace   string
	KubeKind    string
	KubeName    string
	Op          string // created | updated | deleted | unchanged | error
	Error       string
	ContentHash string
	ChangedKeys []string
}

// Drift — kusec ↔ кластер; InCluster=false — kusec запущен вне кластера, сравнения нет.
type Drift struct {
	InCluster bool
	Objects   []DriftObject
}

type DriftObject struct {
	KubeKind         string
	KubeName         string
	Namespace        string
	ExistsInCluster  bool
	Managed          bool
	MissingInCluster []string
	ExtraInCluster   []string
	ValueDiffers     []string
	// NotSyncedSince — изменение в kusec, ещё не применённое в кластер
	NotSyncedSince *time.Time
}

// HasDrift — объект расходится с кластером или содержит неприменённые изменения.
func (o DriftObject) HasDrift() bool {
	return o.NotSyncedSince != nil || !o.ExistsInCluster ||
		len(o.MissingInCluster) > 0 || len(o.ExtraInCluster) > 0 || len(o.ValueDiffers) > 0
}
