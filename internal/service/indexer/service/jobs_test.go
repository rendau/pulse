package service

import (
	"context"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse/internal/constant"
	indexerModel "github.com/mechta-market/pulse/internal/service/indexer/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
)

// TestJobDrafts — образ, который запускается только Job'ами оркестратора (код задач в своей
// репе), становится сервисом с workload'ом-семейством; остальные Job'ы — нет.
func TestJobDrafts(t *testing.T) {
	now := time.Now()
	job := func(ns, name, image, owner string, age time.Duration) k8sModel.Job {
		return k8sModel.Job{Namespace: ns, Name: name, OwnerKind: owner, CreatedAt: now.Add(-age),
			Labels:     map[string]string{"app.kubernetes.io/managed-by": "orchestrator"},
			Containers: []k8sModel.Container{{Name: "task", Image: image}}}
	}
	k8s := &fakeK8sDeps{jobs: []k8sModel.Job{
		job("jobs", "nightly-product-sync-1-9da3", "ghcr.io/org/dags/dags@sha256:aaa", "", time.Hour),
		job("jobs", "nightly-delivery-fetch-1-2249", "ghcr.io/org/dags/dags@sha256:bbb", "", time.Minute),
		job("prod", "payments-api-report-28001", "ghcr.io/org/payments-api:v1", "CronJob", time.Minute), // CronJob
		job("prod", "payments-api-migrate-1", "ghcr.io/org/payments-api:v1", "", time.Minute),           // у сервиса свой workload
		job("jobs", "busybox-debug-1", "busybox:1.36", "", time.Minute),                                 // сторонний образ
	}}
	s := &Service{k8s: k8s, mapper: newImageMapper([]indexerModel.ImageMapping{{Registry: "ghcr.io", RepoTemplate: "https://github.com/{repo}"}})}

	drafts := []*workloadDraft{{serviceKey: "payments-api"}, {serviceKey: "orchestrator"}}
	pods := []k8sModel.Pod{
		{Namespace: "jobs", Name: "nightly-delivery-fetch-1-2249-x1", StartedAt: now,
			Labels:     map[string]string{"batch.kubernetes.io/job-name": "nightly-delivery-fetch-1-2249"},
			Containers: []k8sModel.PodContainer{{Name: "task", ImageID: "ghcr.io/org/dags/dags@sha256:bbb"}}},
		{Namespace: "jobs", Name: "orchestrator-6c84-pg8rz"},
	}

	result := s.jobDrafts(context.Background(), drafts, pods)
	require.Len(t, result, 1)
	d := result[0]
	assert.Equal(t, "dags", d.serviceKey)
	assert.Equal(t, "https://github.com/org/dags", d.repoUrl)
	assert.Equal(t, constant.WorkloadKindJob, d.Kind)
	assert.Equal(t, "jobs", d.Namespace)
	assert.Equal(t, "nightly", d.Name, "общий префикс имён Job'ов")
	assert.Equal(t, "sha256:bbb", d.digest, "digest — из свежего пода семейства")
	assert.Equal(t, "ghcr.io/org/dags/dags@sha256:bbb", d.imageRaw, "образ — последнего Job'а")
	assert.Empty(t, d.Selector)

	names := lo.Map(result, func(d *workloadDraft, _ int) string { return d.Name })
	assert.NotContains(t, names, "payments-api-migrate")
}
