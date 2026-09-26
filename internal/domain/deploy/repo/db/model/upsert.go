package model

import (
	"time"

	domainModel "github.com/rendau/pulse/internal/domain/deploy/model"
)

type Upsert struct {
	PKId  int64
	NewId int64 // id новой записи, заполняется в Create через RETURNING

	Cluster         *string
	Namespace       *string
	Kind            *string
	Name            *string
	ServiceName     *string
	Image           *string
	ImageDigest     *string
	DeployedCommit  *string
	PrevImage       *string
	PrevImageDigest *string
	PrevCommit      *string
	ObservedAt      *time.Time
}

func (m *Upsert) CreateColumnMap() map[string]any {
	result := make(map[string]any, 12)
	if m.Cluster != nil {
		result["cluster"] = *m.Cluster
	}
	if m.Namespace != nil {
		result["namespace"] = *m.Namespace
	}
	if m.Kind != nil {
		result["kind"] = *m.Kind
	}
	if m.Name != nil {
		result["name"] = *m.Name
	}
	if m.ServiceName != nil {
		result["service_name"] = *m.ServiceName
	}
	if m.Image != nil {
		result["image"] = *m.Image
	}
	if m.ImageDigest != nil {
		result["image_digest"] = *m.ImageDigest
	}
	if m.DeployedCommit != nil {
		result["deployed_commit"] = *m.DeployedCommit
	}
	if m.PrevImage != nil {
		result["prev_image"] = *m.PrevImage
	}
	if m.PrevImageDigest != nil {
		result["prev_image_digest"] = *m.PrevImageDigest
	}
	if m.PrevCommit != nil {
		result["prev_commit"] = *m.PrevCommit
	}
	if m.ObservedAt != nil {
		result["observed_at"] = *m.ObservedAt
	}
	return result
}

func (m *Upsert) UpdateColumnMap() map[string]any {
	return m.CreateColumnMap()
}

func (m *Upsert) PKColumnMap() map[string]any {
	return map[string]any{"id": m.PKId}
}

func (m *Upsert) ReturningColumnMap() map[string]any {
	return map[string]any{"id": &m.NewId}
}

// DTO

func DecodeUpsert(v *domainModel.Edit) *Upsert {
	return &Upsert{
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
