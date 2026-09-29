package model

import (
	"time"

	"github.com/samber/lo"

	domainModel "github.com/rendau/pulse/internal/domain/workload/model"
)

// Upsert — модель записи. Составной PK входит в CreateColumnMap, чтобы работал
// UpdateOrCreate (ON CONFLICT (cluster, namespace, kind, name) DO UPDATE).
type Upsert struct {
	PKCluster   string
	PKNamespace string
	PKKind      string
	PKName      string

	ServiceName     *string
	ReplicasDesired *int32
	Image           *string
	ImageDigest     *string
	DeployedCommit  *string
	Selector        *string
	ConfigRefs      *[]string
	Manifest        *domainModel.Manifest
	FirstSeen       *time.Time // только INSERT
	LastSeen        *time.Time
}

func (m *Upsert) CreateColumnMap() map[string]any {
	result := make(map[string]any, 12)
	result["cluster"] = m.PKCluster
	result["namespace"] = m.PKNamespace
	result["kind"] = m.PKKind
	result["name"] = m.PKName
	if m.ServiceName != nil {
		result["service_name"] = *m.ServiceName
	}
	if m.ReplicasDesired != nil {
		result["replicas_desired"] = *m.ReplicasDesired
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
	if m.Selector != nil {
		result["selector"] = *m.Selector
	}
	if m.ConfigRefs != nil {
		result["config_refs"] = *m.ConfigRefs
	}
	if m.Manifest != nil {
		result["manifest_status"] = m.Manifest.Status
		result["manifest_reasons"] = lo.CoalesceSliceOrEmpty(m.Manifest.Reasons)
		result["manifest_service"] = m.Manifest.Service
		result["manifest_port"] = m.Manifest.Port
		result["manifest_tried"] = lo.CoalesceSliceOrEmpty(m.Manifest.Tried)
		result["manifest_digest"] = m.Manifest.Digest
		result["manifest_checked_at"] = m.Manifest.CheckedAt
		// jsonb: nil — NULL (манифеста нет)
		result["manifest"] = lo.Ternary(len(m.Manifest.Raw) > 0, m.Manifest.Raw, nil)
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
	return map[string]any{
		"cluster":   m.PKCluster,
		"namespace": m.PKNamespace,
		"kind":      m.PKKind,
		"name":      m.PKName,
	}
}

func (m *Upsert) ReturningColumnMap() map[string]any {
	return map[string]any{}
}

// DTO

func DecodeUpsert(v *domainModel.Edit, _ int) *Upsert {
	result := &Upsert{
		ServiceName:     v.ServiceName,
		ReplicasDesired: v.ReplicasDesired,
		Image:           v.Image,
		ImageDigest:     v.ImageDigest,
		DeployedCommit:  v.DeployedCommit,
		Selector:        v.Selector,
		ConfigRefs:      v.ConfigRefs,
		Manifest:        v.Manifest,
		FirstSeen:       v.FirstSeen,
		LastSeen:        v.LastSeen,
	}
	if v.Cluster != nil {
		result.PKCluster = *v.Cluster
	}
	if v.Namespace != nil {
		result.PKNamespace = *v.Namespace
	}
	if v.Kind != nil {
		result.PKKind = *v.Kind
	}
	if v.Name != nil {
		result.PKName = *v.Name
	}
	return result
}
