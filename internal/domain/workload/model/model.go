package model

import (
	"time"

	commonModel "github.com/rendau/pulse/internal/domain/common/model"
)

// Main — workload кластера: Deployment / StatefulSet / DaemonSet / CronJob.
// Ключ — (cluster, namespace, kind, name).
type Main struct {
	Cluster         string
	Namespace       string
	Kind            string
	Name            string
	ServiceName     string
	ReplicasDesired int32
	// Image — из спеки контейнера; ImageDigest — что реально запущено (из статуса пода)
	Image          string
	ImageDigest    string
	DeployedCommit string
	// Selector — label-селектор подов, чтобы брать их состояние живьём
	Selector string
	// ConfigRefs — имена configmap/secret шаблона пода (по ним находится приложение kusec)
	ConfigRefs []string
	// Manifest — поиск манифеста сервиса на подах (docs/service-manifest.md)
	Manifest  Manifest
	FirstSeen time.Time
	LastSeen  time.Time
}

// статусы поиска манифеста
const (
	ManifestOk          = "ok"
	ManifestPartial     = "partial"
	ManifestInvalid     = "invalid"
	ManifestAbsent      = "absent"
	ManifestUnreachable = "unreachable"
)

// Manifest — результат поиска манифеста на подах workload'а. Status пуст — ещё не искали.
type Manifest struct {
	Status string
	// Reasons — что отклонено (partial) или почему не принят (invalid, unreachable)
	Reasons []string
	// Port — порт, на котором найден манифест (0 — не найден)
	Port int
	// Tried — какие порты пробовали и что там было («3003: 404», «8080: нет ответа»)
	Tried []string
	// Digest — образ, на котором искали: сменился — ищем заново
	Digest    string
	CheckedAt time.Time
	// Raw — принятый манифест как получен (JSON); разбирается индексером каждый цикл
	Raw []byte
}

// Key — составной первичный ключ.
type Key struct {
	Cluster   string
	Namespace string
	Kind      string
	Name      string
}

func (m *Main) Key() Key {
	return Key{Cluster: m.Cluster, Namespace: m.Namespace, Kind: m.Kind, Name: m.Name}
}

// Edit — мутация (все поля pointer-типы для partial update)
type Edit struct {
	Cluster         *string
	Namespace       *string
	Kind            *string
	Name            *string
	ServiceName     *string
	ReplicasDesired *int32
	Image           *string
	ImageDigest     *string
	DeployedCommit  *string
	Selector        *string
	ConfigRefs      *[]string
	// Manifest — nil: в этом цикле не искали, прошлый результат остаётся
	Manifest *Manifest
	// FirstSeen пишется только при вставке; при обновлении не трогается
	FirstSeen *time.Time
	LastSeen  *time.Time
}

// ListReq — параметры выборки
type ListReq struct {
	commonModel.ListParams

	Cluster      *string
	Namespace    *string
	Kind         *string
	ServiceName  *string
	ServiceNames []string
}
