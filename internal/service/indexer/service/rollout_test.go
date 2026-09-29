package service

import (
	"context"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	deployModel "github.com/rendau/pulse/internal/domain/deploy/model"
	workloadModel "github.com/rendau/pulse/internal/domain/workload/model"
	indexerModel "github.com/rendau/pulse/internal/service/indexer/model"
	k8sModel "github.com/rendau/pulse/internal/service/k8s/model"
)

type fakeDeploy struct{ created []*deployModel.Edit }

func (f *fakeDeploy) Create(_ context.Context, obj *deployModel.Edit) (int64, error) {
	f.created = append(f.created, obj)
	return int64(len(f.created)), nil
}

// rolloutPod — под workload'а: образ из спеки; digest пуст — контейнер ещё не запущен.
func rolloutPod(name, image, digest string, started time.Time) k8sModel.Pod {
	pod := k8sModel.Pod{Namespace: "default", Name: name, Labels: map[string]string{"app": "bot"},
		StartedAt: started, Ready: digest != "", IP: "10.0.0." + name[len(name)-1:],
		Images: map[string]string{"app": image}}
	if digest != "" {
		pod.Containers = []k8sModel.PodContainer{{Name: "app", Image: image, ImageID: "ghcr.io/x@" + digest}}
	} else {
		pod.Containers = []k8sModel.PodContainer{{Name: "app", Image: image, State: "waiting", Reason: "ContainerCreating"}}
	}
	return pod
}

func TestRunningDigest(t *testing.T) {
	now := time.Now()
	const (
		oldImage = "ghcr.io/old/bot:latest"
		newImage = "ghcr.io/new/bot:latest"
	)
	cases := []struct {
		name    string
		image   string
		pods    []k8sModel.Pod
		digest  string
		rolling bool
	}{
		{name: "подов нет (масштаб 0)", image: newImage},
		{name: "обычное состояние", image: newImage,
			pods: []k8sModel.Pod{rolloutPod("bot-1", newImage, "sha256:new", now)}, digest: "sha256:new"},
		{name: "смена образа, старый под жив, новый не запустился — не digest старого", image: newImage,
			pods:    []k8sModel.Pod{rolloutPod("bot-1", oldImage, "sha256:old", now.Add(-time.Hour)), rolloutPod("bot-2", newImage, "", now)},
			rolling: true},
		{name: "смена образа, Recreate: только новый под, ещё не запущен", image: newImage,
			pods: []k8sModel.Pod{rolloutPod("bot-2", newImage, "", now)}, rolling: true},
		{name: "смена образа, новый под запущен, старый ещё жив", image: newImage,
			pods:   []k8sModel.Pod{rolloutPod("bot-1", oldImage, "sha256:old", now.Add(-time.Hour)), rolloutPod("bot-2", newImage, "sha256:new", now)},
			digest: "sha256:new"},
		{name: "тот же тег (keel): новый под запущен — его digest, а не первого попавшегося", image: newImage,
			pods:   []k8sModel.Pod{rolloutPod("bot-1", newImage, "sha256:old", now.Add(-time.Hour)), rolloutPod("bot-2", newImage, "sha256:new", now)},
			digest: "sha256:new"},
		{name: "тот же тег (keel): новый под не запущен — работает прошлая сборка", image: newImage,
			pods:   []k8sModel.Pod{rolloutPod("bot-1", newImage, "sha256:old", now.Add(-time.Hour)), rolloutPod("bot-2", newImage, "", now)},
			digest: "sha256:old"},
		{name: "образ переписан webhook'ом — digest самого свежего пода", image: newImage,
			pods:   []k8sModel.Pod{rolloutPod("bot-1", "mirror.local/new/bot:latest", "sha256:old", now.Add(-time.Hour)), rolloutPod("bot-2", "mirror.local/new/bot:latest", "sha256:new", now)},
			digest: "sha256:new"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			digest, rolling := runningDigest(c.pods, "app", c.image)
			assert.Equal(t, c.digest, digest)
			assert.Equal(t, c.rolling, rolling)
		})
	}
}

// TestRollout — цикл посреди выкатки со сменой образа не пишет новый образ со старым коммитом
// и деплой с чужим или пустым digest: каталог держит прошлое, деплой пишет следующий цикл.
// Манифест (через Service) — когда за Service не осталось подов прошлой сборки.
func TestRollout(t *testing.T) {
	now := time.Now()
	const (
		oldImage = "ghcr.io/old/bot:latest"
		newImage = "ghcr.io/new/bot:latest"
	)
	deploys := &fakeDeploy{}
	caller := &fakeCaller{}
	s := &Service{
		caller: caller,
		k8s: &fakeServices{services: []k8sModel.Service{{Namespace: "default", Name: "bot", Selector: map[string]string{"app": "bot"},
			Ports: []k8sModel.ServicePort{{Name: "system", Port: 3003}}}}},
		conf:   indexerModel.Config{Cluster: "zeon", Manifest: manifestConf},
		mapper: newImageMapper([]indexerModel.ImageMapping{{Registry: "ghcr.io", RepoTemplate: "https://github.com/{repo}"}}),
		deploy: deploys,
	}
	w := k8sModel.Workload{Kind: "Deployment", Namespace: "default", Name: "bot", Selector: "app=bot",
		Containers: []k8sModel.Container{{Name: "app", Image: newImage}}}
	prev := &workloadModel.Main{Cluster: "zeon", Namespace: "default", Kind: "Deployment", Name: "bot",
		ServiceName: "bot", Image: oldImage, ImageDigest: "sha256:old", DeployedCommit: "c-old"}
	previous := map[workloadModel.Key]*workloadModel.Main{prev.Key(): prev}

	cycle := func(pods []k8sModel.Pod) (*workloadDraft, *workloadModel.Edit, int) {
		d := s.newDraft(w, pods)
		s.holdRollouts([]*workloadDraft{d}, previous)
		s.probeManifests(context.Background(), []*workloadDraft{d}, pods, previous, now)
		n := s.recordDeploys(context.Background(), []*workloadDraft{d}, previous)
		return d, d.toEdit(s.conf.Cluster, now), n
	}

	// 1. старый под ещё работает, новый создан, но образ ещё качается
	d, edit, n := cycle([]k8sModel.Pod{rolloutPod("bot-1", oldImage, "sha256:old", now.Add(-time.Hour)), rolloutPod("bot-2", newImage, "", now)})
	assert.True(t, d.rolling)
	assert.Zero(t, n, "деплой — только когда новый под запустился")
	assert.Equal(t, oldImage, lo.FromPtr(edit.Image), "в каталоге — то, что работает")
	assert.Nil(t, edit.ImageDigest, "digest не трогаем")
	assert.Nil(t, edit.DeployedCommit, "коммит не трогаем")
	assert.Empty(t, caller.calls, "манифест во время выкатки не ищем: старый под отдал бы прошлую сборку")

	// 2. новый под запущен (старый ещё завершается): деплой с новым digest
	d, edit, n = cycle([]k8sModel.Pod{rolloutPod("bot-1", oldImage, "sha256:old", now.Add(-time.Hour)), rolloutPod("bot-2", newImage, "sha256:new", now)})
	assert.False(t, d.rolling)
	require.Equal(t, 1, n)
	rec := deploys.created[0]
	assert.Equal(t, newImage, lo.FromPtr(rec.Image))
	assert.Equal(t, "sha256:new", lo.FromPtr(rec.ImageDigest))
	assert.Equal(t, oldImage, lo.FromPtr(rec.PrevImage))
	assert.Equal(t, "sha256:old", lo.FromPtr(rec.PrevImageDigest))
	assert.Equal(t, "c-old", lo.FromPtr(rec.PrevCommit))
	assert.Equal(t, newImage, lo.FromPtr(edit.Image))
	assert.Equal(t, "sha256:new", lo.FromPtr(edit.ImageDigest))

	assert.Empty(t, caller.calls, "старый под ещё за Service — ответить мог бы он")

	// 3. старый под завершился: манифест — через Service
	prev.Image, prev.ImageDigest = newImage, "sha256:new"
	_, _, n = cycle([]k8sModel.Pod{rolloutPod("bot-2", newImage, "sha256:new", now)})
	assert.Zero(t, n)
	assert.Equal(t, []string{"default/bot:3003/.well-known/pulse"}, caller.calls)
}
