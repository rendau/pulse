package model

import (
	"encoding/json"
	"time"

	domainModel "github.com/mechta-market/pulse/internal/domain/svc/model"
)

// Upsert — модель записи. PK (name) входит в CreateColumnMap, чтобы работал
// UpdateOrCreate (ON CONFLICT (name) DO UPDATE): каталог пишется только upsert'ом.
type Upsert struct {
	PKName string

	Title           *string
	RepoUrl         *string
	Description     *string
	Criticality     *string
	OwnerTeam       *string
	OwnerContacts   *[]string
	Aliases         *[]string
	ClusterNames    *[]string
	MetadataPresent *bool
	Metadata        []byte     // jsonb, ручная сериализация через metadataJSON; nil — не трогать
	FirstSeen       *time.Time // только INSERT
	LastSeen        *time.Time
}

func (m *Upsert) CreateColumnMap() map[string]any {
	result := make(map[string]any, 12)
	result["name"] = m.PKName
	if m.Title != nil {
		result["title"] = *m.Title
	}
	if m.RepoUrl != nil {
		result["repo_url"] = *m.RepoUrl
	}
	if m.Description != nil {
		result["description"] = *m.Description
	}
	if m.Criticality != nil {
		result["criticality"] = *m.Criticality
	}
	if m.OwnerTeam != nil {
		result["owner_team"] = *m.OwnerTeam
	}
	if m.OwnerContacts != nil {
		result["owner_contacts"] = *m.OwnerContacts
	}
	if m.Aliases != nil {
		result["aliases"] = *m.Aliases
	}
	if m.ClusterNames != nil {
		result["cluster_names"] = *m.ClusterNames
	}
	if m.MetadataPresent != nil {
		result["metadata_present"] = *m.MetadataPresent
	}
	if m.Metadata != nil {
		result["metadata"] = m.Metadata
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
	delete(result, "first_seen") // момент первого появления не перезаписывается
	return result
}

func (m *Upsert) PKColumnMap() map[string]any {
	return map[string]any{"name": m.PKName}
}

func (m *Upsert) ReturningColumnMap() map[string]any {
	return map[string]any{}
}

// DTO

func DecodeUpsert(v *domainModel.Edit) (*Upsert, error) {
	result := &Upsert{
		Title:           v.Title,
		RepoUrl:         v.RepoUrl,
		Description:     v.Description,
		Criticality:     v.Criticality,
		OwnerTeam:       v.OwnerTeam,
		OwnerContacts:   v.OwnerContacts,
		Aliases:         v.Aliases,
		ClusterNames:    v.ClusterNames,
		MetadataPresent: v.MetadataPresent,
		FirstSeen:       v.FirstSeen,
		LastSeen:        v.LastSeen,
	}
	if v.Name != nil {
		result.PKName = *v.Name
	}

	if v.Metadata != nil {
		raw, err := json.Marshal(decodeMetadata(v.Metadata))
		if err != nil {
			return nil, err
		}
		result.Metadata = raw
	}

	return result, nil
}
