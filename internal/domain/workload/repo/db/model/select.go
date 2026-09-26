package model

import (
	"time"

	"github.com/samber/lo"

	domainModel "github.com/rendau/pulse/internal/domain/workload/model"
)

type Select struct {
	Cluster         string
	Namespace       string
	Kind            string
	Name            string
	ServiceName     string
	ReplicasDesired int32
	Image           string
	ImageDigest     string
	DeployedCommit  string
	Selector        string
	ConfigRefs      []string

	ManifestStatus    string
	ManifestReasons   []string
	ManifestPort      int
	ManifestTried     []string
	ManifestDigest    string
	ManifestCheckedAt *time.Time
	Manifest          []byte // jsonb как есть

	FirstSeen time.Time
	LastSeen  time.Time
}

func (m *Select) ListColumnMap() map[string]any {
	return map[string]any{
		"cluster":             &m.Cluster,
		"namespace":           &m.Namespace,
		"kind":                &m.Kind,
		"name":                &m.Name,
		"service_name":        &m.ServiceName,
		"replicas_desired":    &m.ReplicasDesired,
		"image":               &m.Image,
		"image_digest":        &m.ImageDigest,
		"deployed_commit":     &m.DeployedCommit,
		"selector":            &m.Selector,
		"config_refs":         &m.ConfigRefs,
		"manifest_status":     &m.ManifestStatus,
		"manifest_reasons":    &m.ManifestReasons,
		"manifest_port":       &m.ManifestPort,
		"manifest_tried":      &m.ManifestTried,
		"manifest_digest":     &m.ManifestDigest,
		"manifest_checked_at": &m.ManifestCheckedAt,
		"manifest":            &m.Manifest,
		"first_seen":          &m.FirstSeen,
		"last_seen":           &m.LastSeen,
	}
}

func (m *Select) PKColumnMap() map[string]any {
	return map[string]any{
		"cluster":   m.Cluster,
		"namespace": m.Namespace,
		"kind":      m.Kind,
		"name":      m.Name,
	}
}

func (m *Select) DefaultSortColumns() []string {
	return []string{"namespace", "kind", "name"}
}

// DTO

func EncodeSelect(v *Select, _ int) *domainModel.Main {
	return &domainModel.Main{
		Cluster:         v.Cluster,
		Namespace:       v.Namespace,
		Kind:            v.Kind,
		Name:            v.Name,
		ServiceName:     v.ServiceName,
		ReplicasDesired: v.ReplicasDesired,
		Image:           v.Image,
		ImageDigest:     v.ImageDigest,
		DeployedCommit:  v.DeployedCommit,
		Selector:        v.Selector,
		ConfigRefs:      v.ConfigRefs,
		Manifest: domainModel.Manifest{
			Status:    v.ManifestStatus,
			Reasons:   v.ManifestReasons,
			Port:      v.ManifestPort,
			Tried:     v.ManifestTried,
			Digest:    v.ManifestDigest,
			CheckedAt: lo.FromPtr(v.ManifestCheckedAt),
			Raw:       v.Manifest,
		},
		FirstSeen: v.FirstSeen,
		LastSeen:  v.LastSeen,
	}
}
