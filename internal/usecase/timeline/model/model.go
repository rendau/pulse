package model

import (
	"time"

	deployModel "github.com/rendau/pulse/internal/domain/deploy/model"
	eventModel "github.com/rendau/pulse/internal/domain/event/model"
)

const ScopeCluster = "cluster"

// TimelineReq — сервис(ы) либо scope=cluster.
type TimelineReq struct {
	Services []string
	Scope    string
	Window   time.Duration
}

type TimelineResult struct {
	Services   []string
	Window     time.Duration
	Events     []eventModel.Event
	TotalCount int
	Truncated  bool
	Errors     []SourceError
}

// Commit — коммит ветки по умолчанию.
type Commit struct {
	SHA     string
	Author  string
	Message string
	Date    time.Time
	Url     string
}

// Unreleased — смержено, но ещё не в проде.
type Unreleased struct {
	DeployedCommit string
	BehindBy       int
	Commits        []Commit
}

// источники изменений конфигурации
const (
	// ConfigChangeSourceKusec — правка в kusec (аудит): ещё не в кластере, пока её не применил sync
	ConfigChangeSourceKusec = "kusec"
	// ConfigChangeSourceKusecSync — sync kusec применил объект в кластер без выкатки подов
	ConfigChangeSourceKusecSync = "kusec_sync"
	// ConfigChangeSourceReloader — выкатка reloader'а после смены объекта; если найден sync
	// kusec, известны автор и изменённые ключи
	ConfigChangeSourceReloader = "reloader"
)

// ConfigChange — изменение конфигурации с уже применённым маскированием.
type ConfigChange struct {
	TS     time.Time
	Source string // kusec | kusec_sync | reloader
	// Action — create | update | delete | activate | deactivate | import (kusec);
	// created | updated | deleted (kusec_sync); rollout (reloader)
	Action   string
	Kind     string // configmap | secret | app
	Object   string // kusec-caravan-main
	Key      string // ключ (kusec); пусто — объект целиком
	OldValue string // значение (обычный конфиг, маскированное) или отпечаток (reloader); секрет — ***
	NewValue string
	Fields   []string
	// ChangedKeys — ключи, применённые sync'ом (kusec_sync, reloader)
	ChangedKeys []string
	Author      string
	Workload    string // namespace/name — для reloader
	SyncRunId   string
	Status      string // статус sync, если не ok
}

// UnsyncedConfig — расхождение конфигурации в kusec и в кластере: правка не применена sync'ом
// или содержимое объекта в кластере отличается (только имена ключей).
type UnsyncedConfig struct {
	Kind             string
	Object           string
	NotSyncedSince   *time.Time
	ExistsInCluster  bool
	MissingInCluster []string
	ExtraInCluster   []string
	ValueDiffers     []string
}

type ChangesResult struct {
	Service string
	Window  time.Duration
	Commits []Commit
	// LastCommit — последний коммит ветки по умолчанию, если за окно коммитов нет
	LastCommit    *Commit
	Unreleased    *Unreleased
	Deploys       []*deployModel.Main
	ConfigChanges []ConfigChange
	// UnsyncedConfig — отсутствует, если kusec не подключён
	UnsyncedConfig []UnsyncedConfig
	Errors         []SourceError
}

type SourceError struct {
	Source  string
	Message string
}
