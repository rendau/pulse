package model

import (
	"encoding/json"
	"log/slog"
	"time"

	domainModel "github.com/mechta-market/pulse/internal/domain/svc/model"
)

type Select struct {
	Name            string
	Title           string
	RepoUrl         string
	Description     string
	Criticality     string
	OwnerTeam       string
	OwnerContacts   []string
	Aliases         []string
	ClusterNames    []string
	MetadataPresent bool
	Metadata        []byte // jsonb, ручная десериализация через metadataJSON
	FirstSeen       time.Time
	LastSeen        time.Time
}

func (m *Select) ListColumnMap() map[string]any {
	return map[string]any{
		"name":             &m.Name,
		"title":            &m.Title,
		"repo_url":         &m.RepoUrl,
		"description":      &m.Description,
		"criticality":      &m.Criticality,
		"owner_team":       &m.OwnerTeam,
		"owner_contacts":   &m.OwnerContacts,
		"aliases":          &m.Aliases,
		"cluster_names":    &m.ClusterNames,
		"metadata_present": &m.MetadataPresent,
		"metadata":         &m.Metadata,
		"first_seen":       &m.FirstSeen,
		"last_seen":        &m.LastSeen,
	}
}

func (m *Select) PKColumnMap() map[string]any {
	return map[string]any{"name": m.Name}
}

func (m *Select) DefaultSortColumns() []string {
	return []string{"name"}
}

// DTO

func EncodeSelect(v *Select, _ int) *domainModel.Main {
	result := &domainModel.Main{
		Name:            v.Name,
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

	if len(v.Metadata) > 0 {
		var meta metadataJSON
		if err := json.Unmarshal(v.Metadata, &meta); err != nil {
			// повреждённый jsonb не должен ронять выборку каталога
			slog.Warn("service metadata decode failed", "service", v.Name, "error", err)
		}
		result.Metadata = encodeMetadata(meta)
	}

	return result
}
