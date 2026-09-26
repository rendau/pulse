package model

import (
	"time"

	domainModel "github.com/rendau/pulse/internal/domain/dependency/model"
)

type Select struct {
	Cluster     string
	FromService string
	ToService   string
	ToHost      string
	Port        int32
	Scheme      string
	Source      string
	Key         string
	FirstSeen   time.Time
	LastSeen    time.Time
}

func (m *Select) ListColumnMap() map[string]any {
	return map[string]any{
		"cluster":      &m.Cluster,
		"from_service": &m.FromService,
		"to_service":   &m.ToService,
		"to_host":      &m.ToHost,
		"port":         &m.Port,
		"scheme":       &m.Scheme,
		"source":       &m.Source,
		"key":          &m.Key,
		"first_seen":   &m.FirstSeen,
		"last_seen":    &m.LastSeen,
	}
}

func (m *Select) PKColumnMap() map[string]any {
	return map[string]any{"cluster": m.Cluster, "from_service": m.FromService, "to_host": m.ToHost, "port": m.Port, "key": m.Key}
}

func (m *Select) DefaultSortColumns() []string {
	return []string{"from_service", "to_host", "port", "key"}
}

// DTO

func EncodeSelect(v *Select, _ int) *domainModel.Main {
	return &domainModel.Main{
		Cluster: v.Cluster, FromService: v.FromService, ToService: v.ToService, ToHost: v.ToHost,
		Port: v.Port, Scheme: v.Scheme, Source: v.Source, Key: v.Key, FirstSeen: v.FirstSeen, LastSeen: v.LastSeen,
	}
}
