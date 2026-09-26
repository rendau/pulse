package model

import (
	"time"

	domainModel "github.com/rendau/pulse/internal/domain/deploy/model"
)

type Select struct {
	Id              int64
	Cluster         string
	Namespace       string
	Kind            string
	Name            string
	ServiceName     string
	Image           string
	ImageDigest     string
	DeployedCommit  string
	PrevImage       string
	PrevImageDigest string
	PrevCommit      string
	ObservedAt      time.Time
}

func (m *Select) ListColumnMap() map[string]any {
	return map[string]any{
		"id":                &m.Id,
		"cluster":           &m.Cluster,
		"namespace":         &m.Namespace,
		"kind":              &m.Kind,
		"name":              &m.Name,
		"service_name":      &m.ServiceName,
		"image":             &m.Image,
		"image_digest":      &m.ImageDigest,
		"deployed_commit":   &m.DeployedCommit,
		"prev_image":        &m.PrevImage,
		"prev_image_digest": &m.PrevImageDigest,
		"prev_commit":       &m.PrevCommit,
		"observed_at":       &m.ObservedAt,
	}
}

func (m *Select) PKColumnMap() map[string]any {
	return map[string]any{"id": m.Id}
}

func (m *Select) DefaultSortColumns() []string {
	return []string{"observed_at desc", "id desc"}
}

// DTO

func EncodeSelect(v *Select, _ int) *domainModel.Main {
	return &domainModel.Main{
		Id:              v.Id,
		Cluster:         v.Cluster,
		Namespace:       v.Namespace,
		Kind:            v.Kind,
		Name:            v.Name,
		ServiceName:     v.ServiceName,
		Image:           v.Image,
		ImageDigest:     v.ImageDigest,
		DeployedCommit:  v.DeployedCommit,
		PrevImage:       v.PrevImage,
		PrevImageDigest: v.PrevImageDigest,
		PrevCommit:      v.PrevCommit,
		ObservedAt:      v.ObservedAt,
	}
}
