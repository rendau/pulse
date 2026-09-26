package model

import (
	"time"

	domainModel "github.com/rendau/pulse/internal/domain/dependency/model"
)

// Upsert — модель записи; составной PK входит в CreateColumnMap для UpdateOrCreate.
type Upsert struct {
	PKCluster     string
	PKFromService string
	PKToHost      string
	PKPort        int32
	PKKey         string

	ToService *string
	Scheme    *string
	Source    *string
	FirstSeen *time.Time // только INSERT
	LastSeen  *time.Time
}

func (m *Upsert) CreateColumnMap() map[string]any {
	result := make(map[string]any, 10)
	result["cluster"] = m.PKCluster
	result["from_service"] = m.PKFromService
	result["to_host"] = m.PKToHost
	result["port"] = m.PKPort
	result["key"] = m.PKKey
	if m.ToService != nil {
		result["to_service"] = *m.ToService
	}
	if m.Scheme != nil {
		result["scheme"] = *m.Scheme
	}
	if m.Source != nil {
		result["source"] = *m.Source
	}
	if m.FirstSeen != nil {
		result["first_seen"] = *m.FirstSeen
	}
	if m.LastSeen != nil {
		result["last_seen"] = *m.LastSeen
	}
	return result
}

func (m *Upsert) UpdateColumnMap() map[string]any {
	result := m.CreateColumnMap()
	for k := range m.PKColumnMap() {
		delete(result, k)
	}
	delete(result, "first_seen")
	return result
}

func (m *Upsert) PKColumnMap() map[string]any {
	return map[string]any{"cluster": m.PKCluster, "from_service": m.PKFromService, "to_host": m.PKToHost, "port": m.PKPort, "key": m.PKKey}
}

func (m *Upsert) ReturningColumnMap() map[string]any {
	return map[string]any{}
}

// DTO

func DecodeUpsert(v *domainModel.Edit, _ int) *Upsert {
	result := &Upsert{ToService: v.ToService, Scheme: v.Scheme, Source: v.Source, FirstSeen: v.FirstSeen, LastSeen: v.LastSeen}
	if v.Cluster != nil {
		result.PKCluster = *v.Cluster
	}
	if v.FromService != nil {
		result.PKFromService = *v.FromService
	}
	if v.ToHost != nil {
		result.PKToHost = *v.ToHost
	}
	if v.Port != nil {
		result.PKPort = *v.Port
	}
	if v.Key != nil {
		result.PKKey = *v.Key
	}
	return result
}
