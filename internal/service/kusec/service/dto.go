package service

import (
	"time"

	"github.com/samber/lo"

	kusecModel "github.com/rendau/pulse/internal/service/kusec/model"
)

// Транспортные модели kusec (protojson): int64 приходят строками, optional-поля без значения
// в JSON отсутствуют.

type resolveRep struct {
	Found bool `json:"found"`
	App   struct {
		Id        string `json:"id"`
		Namespace string `json:"namespace"`
		SlugName  string `json:"slug_name"`
	} `json:"app"`
	KubeKind string `json:"kube_kind"`
	ObjectId string `json:"object_id"`
}

func (r *resolveRep) decode() *kusecModel.Resolved {
	if !r.Found {
		return &kusecModel.Resolved{}
	}
	return &kusecModel.Resolved{
		Found: true, AppId: r.App.Id, AppSlug: r.App.SlugName, Namespace: r.App.Namespace,
		KubeKind: r.KubeKind, ObjectId: r.ObjectId,
	}
}

type auditListRep struct {
	Results []auditRep `json:"results"`
}

type auditRep struct {
	Id         int64            `json:"id,string"`
	CreatedAt  time.Time        `json:"created_at"`
	ActorName  string           `json:"actor_name"`
	Source     string           `json:"source"`
	EntityType string           `json:"entity_type"`
	EntityId   string           `json:"entity_id"`
	AppId      string           `json:"app_id"`
	Namespace  string           `json:"namespace"`
	AppSlug    string           `json:"app_slug"`
	KubeKind   string           `json:"kube_kind"`
	KubeName   string           `json:"kube_name"`
	Key        string           `json:"key"`
	Action     string           `json:"action"`
	BatchId    string           `json:"batch_id"`
	Changes    []auditChangeRep `json:"changes"`
}

type auditChangeRep struct {
	Field     string  `json:"field"`
	Old       *string `json:"old"`
	New       *string `json:"new"`
	OldHash   string  `json:"old_hash"`
	NewHash   string  `json:"new_hash"`
	OldSize   *int64  `json:"old_size,string"`
	NewSize   *int64  `json:"new_size,string"`
	Truncated bool    `json:"truncated"`
}

func decodeAuditEntry(v auditRep, _ int) kusecModel.AuditEntry {
	return kusecModel.AuditEntry{
		Id: v.Id, CreatedAt: v.CreatedAt, ActorName: v.ActorName, Source: v.Source,
		EntityType: v.EntityType, EntityId: v.EntityId, AppId: v.AppId, Namespace: v.Namespace, AppSlug: v.AppSlug,
		KubeKind: v.KubeKind, KubeName: v.KubeName, Key: v.Key, Action: v.Action, BatchId: v.BatchId,
		Changes: lo.Map(v.Changes, func(c auditChangeRep, _ int) kusecModel.AuditChange {
			return kusecModel.AuditChange{
				Field: c.Field, Old: c.Old, New: c.New, OldHash: c.OldHash, NewHash: c.NewHash,
				OldSize: c.OldSize, NewSize: c.NewSize, Truncated: c.Truncated,
			}
		}),
	}
}

type syncRunListRep struct {
	Results []syncRunRep `json:"results"`
}

type syncRunRep struct {
	Id         string             `json:"id"`
	StartedAt  time.Time          `json:"started_at"`
	FinishedAt *time.Time         `json:"finished_at"`
	Status     string             `json:"status"`
	Error      string             `json:"error"`
	ActorName  string             `json:"actor_name"`
	Source     string             `json:"source"`
	AppId      string             `json:"app_id"`
	DurationMs int64              `json:"duration_ms,string"`
	Objects    []syncRunObjectRep `json:"objects"`
}

type syncRunObjectRep struct {
	Namespace   string   `json:"namespace"`
	KubeKind    string   `json:"kube_kind"`
	KubeName    string   `json:"kube_name"`
	Op          string   `json:"op"`
	Error       string   `json:"error"`
	ContentHash string   `json:"content_hash"`
	ChangedKeys []string `json:"changed_keys"`
}

func decodeSyncRun(v syncRunRep, _ int) kusecModel.SyncRun {
	return kusecModel.SyncRun{
		Id: v.Id, StartedAt: v.StartedAt, FinishedAt: v.FinishedAt, Status: v.Status, Error: v.Error,
		ActorName: v.ActorName, Source: v.Source, AppId: v.AppId, DurationMs: v.DurationMs,
		Objects: lo.Map(v.Objects, func(o syncRunObjectRep, _ int) kusecModel.SyncObject {
			return kusecModel.SyncObject{
				Namespace: o.Namespace, KubeKind: o.KubeKind, KubeName: o.KubeName, Op: o.Op, Error: o.Error,
				ContentHash: o.ContentHash, ChangedKeys: o.ChangedKeys,
			}
		}),
	}
}

type driftRep struct {
	InCluster bool             `json:"in_cluster"`
	Objects   []driftObjectRep `json:"objects"`
}

type driftObjectRep struct {
	KubeKind         string     `json:"kube_kind"`
	KubeName         string     `json:"kube_name"`
	Namespace        string     `json:"namespace"`
	ExistsInCluster  bool       `json:"exists_in_cluster"`
	Managed          bool       `json:"managed"`
	MissingInCluster []string   `json:"missing_in_cluster"`
	ExtraInCluster   []string   `json:"extra_in_cluster"`
	ValueDiffers     []string   `json:"value_differs"`
	NotSyncedSince   *time.Time `json:"not_synced_since"`
}

func decodeDriftObject(v driftObjectRep, _ int) kusecModel.DriftObject {
	return kusecModel.DriftObject{
		KubeKind: v.KubeKind, KubeName: v.KubeName, Namespace: v.Namespace,
		ExistsInCluster: v.ExistsInCluster, Managed: v.Managed,
		MissingInCluster: v.MissingInCluster, ExtraInCluster: v.ExtraInCluster, ValueDiffers: v.ValueDiffers,
		NotSyncedSince: v.NotSyncedSince,
	}
}
