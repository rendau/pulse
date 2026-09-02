package model

import (
	"time"

	commonModel "github.com/mechta-market/pulse/internal/domain/common/model"
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
	Selector  string
	FirstSeen time.Time
	LastSeen  time.Time
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
