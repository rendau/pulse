package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	svcModel "github.com/rendau/pulse/internal/domain/svc/model"
	workloadModel "github.com/rendau/pulse/internal/domain/workload/model"
	indexerModel "github.com/rendau/pulse/internal/service/indexer/model"
	localModel "github.com/rendau/pulse/internal/service/indexer/service/model"
	k8sModel "github.com/rendau/pulse/internal/service/k8s/model"
	svcproxyModel "github.com/rendau/pulse/internal/service/svcproxy/model"
)

var manifestConf = indexerModel.ManifestConfig{
	Path: "/.well-known/pulse", ServicePort: "system", RefreshAfter: 24 * time.Hour, RetryAfter: time.Hour,
}

// fakeCaller — Service отвечают по «service:port»: код ответа и тело; нет ключа — не отвечает.
type fakeCaller struct {
	mu      sync.Mutex
	answers map[string]*svcproxyModel.Response
	calls   []string
}

func (f *fakeCaller) GetService(_ context.Context, t svcproxyModel.ServiceTarget, path string, _, headers map[string]string, _ int64) (*svcproxyModel.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("%s/%s:%d%s", t.Namespace, t.Service, t.Port, path))
	if headers["X-Pulse-Request-Id"] == "" {
		return nil, errors.New("no request id")
	}
	if resp, ok := f.answers[fmt.Sprintf("%s:%d", t.Service, t.Port)]; ok {
		return resp, nil
	}
	return nil, errors.New("connection refused")
}

// fakeServices — k8s Service кластера для поиска манифеста.
type fakeServices struct {
	k8sClientI
	services []k8sModel.Service
	err      error
}

func (f *fakeServices) ListServices(context.Context, string) ([]k8sModel.Service, error) {
	return f.services, f.err
}

// Порт манифеста — только порт system у Service, который ведёт на поды workload'а; таких
// несколько — первый по имени.
func TestManifestTarget(t *testing.T) {
	s := &Service{conf: indexerModel.Config{Manifest: manifestConf}}
	pod := k8sModel.Pod{Namespace: "prod", Name: "caravan-1", Labels: map[string]string{"app": "caravan", "tier": "api"}}
	system := k8sModel.ServicePort{Name: "system", Port: 3003, TargetPort: "3003"}
	http := k8sModel.ServicePort{Name: "http", Port: 80, TargetPort: "8080"}

	cases := []struct {
		name     string
		services []k8sModel.Service
		want     string
	}{
		{name: "основной и служебный порты", want: "caravan:3003", services: []k8sModel.Service{
			{Namespace: "prod", Name: "caravan", Selector: map[string]string{"app": "caravan"}, Ports: []k8sModel.ServicePort{http, system}},
		}},
		{name: "служебного порта в Service нет — основной не трогаем", services: []k8sModel.Service{
			{Namespace: "prod", Name: "caravan", Selector: map[string]string{"app": "caravan"}, Ports: []k8sModel.ServicePort{http}},
		}},
		{name: "чужие: другой namespace, другой селектор, без селектора", services: []k8sModel.Service{
			{Namespace: "stage", Name: "caravan", Selector: map[string]string{"app": "caravan"}, Ports: []k8sModel.ServicePort{system}},
			{Namespace: "prod", Name: "front", Selector: map[string]string{"app": "front"}, Ports: []k8sModel.ServicePort{system}},
			{Namespace: "prod", Name: "external", Ports: []k8sModel.ServicePort{system}},
		}},
		{name: "несколько — первый по имени", want: "caravan-sys:4004", services: []k8sModel.Service{
			{Namespace: "prod", Name: "caravan-tier", Selector: map[string]string{"tier": "api"}, Ports: []k8sModel.ServicePort{{Name: "system", Port: 5005}}},
			{Namespace: "prod", Name: "caravan-sys", Selector: map[string]string{"app": "caravan"}, Ports: []k8sModel.ServicePort{{Name: "system", Port: 4004}}},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			target, found := s.manifestTarget([]k8sModel.Pod{pod}, c.services)
			assert.Equal(t, c.want != "", found)
			if found {
				assert.Equal(t, c.want, fmt.Sprintf("%s:%d", target.Service, target.Port))
				assert.Equal(t, "prod", target.Namespace)
			}
		})
	}

	_, found := s.manifestTarget(nil, []k8sModel.Service{{Namespace: "prod", Name: "caravan", Selector: map[string]string{"app": "caravan"}, Ports: []k8sModel.ServicePort{system}}})
	assert.False(t, found, "готовых подов нет — Service ответить некому")
}

func TestProbeDue(t *testing.T) {
	s := &Service{conf: indexerModel.Config{Manifest: manifestConf}}
	now := time.Now()
	target := svcproxyModel.ServiceTarget{Namespace: "prod", Service: "caravan", Port: 3003}
	probed := func(status, digest string, ago time.Duration) *workloadModel.Manifest {
		return &workloadModel.Manifest{Status: status, Service: "caravan", Port: 3003, Digest: digest, CheckedAt: now.Add(-ago)}
	}

	assert.True(t, s.probeDue(nil, "sha256:a", target, now), "ещё не искали")
	assert.True(t, s.probeDue(probed("ok", "sha256:a", time.Minute), "sha256:b", target, now), "выкатка")
	assert.False(t, s.probeDue(probed("ok", "sha256:a", time.Hour), "sha256:a", target, now), "принятый — раз в сутки")
	assert.True(t, s.probeDue(probed("partial", "sha256:a", 25*time.Hour), "sha256:a", target, now))
	assert.False(t, s.probeDue(probed("absent", "sha256:a", 30*time.Minute), "sha256:a", target, now), "неудача — раз в час")
	assert.True(t, s.probeDue(probed("invalid", "sha256:a", 2*time.Hour), "sha256:a", target, now))
	assert.False(t, s.probeDue(probed("ok", "sha256:a", time.Hour), "", target, now), "digest неизвестен — не выкатка")

	// Service появился, пропал или сменил порт — сразу
	noService := &workloadModel.Manifest{Status: "absent", Digest: "sha256:a", CheckedAt: now.Add(-time.Minute)}
	assert.True(t, s.probeDue(noService, "sha256:a", target, now), "в Service добавили служебный порт")
	assert.False(t, s.probeDue(noService, "sha256:a", svcproxyModel.ServiceTarget{}, now))
	assert.True(t, s.probeDue(probed("ok", "sha256:a", time.Minute), "sha256:a", svcproxyModel.ServiceTarget{}, now), "Service пропал")
	assert.True(t, s.probeDue(probed("ok", "sha256:a", time.Minute), "sha256:a", svcproxyModel.ServiceTarget{Namespace: "prod", Service: "caravan", Port: 3013}, now))
}

func TestProbe(t *testing.T) {
	raw, err := os.ReadFile("model/testdata/manifest.json")
	require.NoError(t, err)

	caller := &fakeCaller{answers: map[string]*svcproxyModel.Response{"ocenter:3003": {StatusCode: 200, Body: raw}}}
	s := &Service{conf: indexerModel.Config{Manifest: manifestConf}, caller: caller}
	target := svcproxyModel.ServiceTarget{Namespace: "prod", Service: "ocenter", Port: 3003}

	result, parsed := s.probe(context.Background(), target)
	require.NotNil(t, parsed)
	assert.Equal(t, workloadModel.ManifestOk, result.Status)
	assert.Equal(t, "ocenter", result.Service)
	assert.Equal(t, 3003, result.Port)
	assert.Equal(t, []string{"ocenter:3003: манифест"}, result.Tried)
	assert.Equal(t, raw, result.Raw)
	assert.Equal(t, []string{"prod/ocenter:3003/.well-known/pulse"}, caller.calls, "через Service, одним запросом")

	answer := func(resp *svcproxyModel.Response) (*workloadModel.Manifest, *localModel.ParsedManifest) {
		caller.answers["ocenter:3003"] = resp
		return s.probe(context.Background(), target)
	}

	result, parsed = answer(&svcproxyModel.Response{StatusCode: 404})
	assert.Nil(t, parsed)
	assert.Equal(t, workloadModel.ManifestAbsent, result.Status, "HTTP-сервер ответил 404 — манифеста нет")
	assert.Equal(t, []string{"ocenter:3003: 404"}, result.Tried)
	assert.Equal(t, 3003, result.Port, "куда смотрели — видно и при неудаче")

	// 200 на любой путь (SPA) — не манифест; большая страница (обрезана по лимиту) — тоже
	result, _ = answer(&svcproxyModel.Response{StatusCode: 200, Body: []byte(`{"status":"ok"}`)})
	assert.Equal(t, workloadModel.ManifestAbsent, result.Status)
	assert.Equal(t, []string{"ocenter:3003: 200, не манифест"}, result.Tried)
	result, _ = answer(&svcproxyModel.Response{StatusCode: 200, Body: []byte("<html>…"), Truncated: true})
	assert.Equal(t, workloadModel.ManifestAbsent, result.Status)

	// а обрезанный манифест — invalid
	result, _ = answer(&svcproxyModel.Response{StatusCode: 200, Body: []byte(`{"pulse_manifest": 1, "service": {`), Truncated: true})
	assert.Equal(t, workloadModel.ManifestInvalid, result.Status)

	// невалидный манифест
	result, parsed = answer(&svcproxyModel.Response{StatusCode: 200, Body: []byte(`{"pulse_manifest":1}`)})
	assert.Nil(t, parsed)
	assert.Equal(t, workloadModel.ManifestInvalid, result.Status)
	assert.NotEmpty(t, result.Reasons)
	assert.Empty(t, result.Raw)

	// Service не ответил
	delete(caller.answers, "ocenter:3003")
	result, _ = s.probe(context.Background(), target)
	assert.Equal(t, workloadModel.ManifestUnreachable, result.Status)
	assert.Equal(t, []string{"ocenter:3003: нет ответа"}, result.Tried)
}

// Цикл индексера: манифест — только через Service с портом system; во время выкатки и без
// списка Service — прошлый результат; Service появился — ищем сразу.
func TestProbeManifests(t *testing.T) {
	raw, err := os.ReadFile("model/testdata/manifest.json")
	require.NoError(t, err)

	now := time.Now()
	pod := func(name, digest string) k8sModel.Pod {
		return k8sModel.Pod{Namespace: "prod", Name: name, IP: "10.0.0.7", Ready: true, Labels: map[string]string{"app": "proxy"},
			Containers: []k8sModel.PodContainer{{Name: "app", ImageID: "ghcr.io/x/proxy@" + digest}}}
	}
	withSystem := k8sModel.Service{Namespace: "prod", Name: "proxy", Selector: map[string]string{"app": "proxy"}, Ports: []k8sModel.ServicePort{
		{Name: "http", Port: 80, TargetPort: "8080"}, {Name: "system", Port: 3003, TargetPort: "3003"},
	}}
	withoutSystem := k8sModel.Service{Namespace: "prod", Name: "proxy", Selector: map[string]string{"app": "proxy"}, Ports: []k8sModel.ServicePort{
		{Name: "http", Port: 80, TargetPort: "8080"},
	}}

	caller := &fakeCaller{answers: map[string]*svcproxyModel.Response{"proxy:3003": {StatusCode: 200, Body: raw}}}
	k8s := &fakeServices{services: []k8sModel.Service{withSystem}}
	s := &Service{conf: indexerModel.Config{Cluster: "zeon", Manifest: manifestConf}, caller: caller, k8s: k8s}

	cycle := func(prev workloadModel.Manifest, pods ...k8sModel.Pod) *workloadDraft {
		caller.calls = nil
		d := &workloadDraft{Workload: k8sModel.Workload{Kind: "Deployment", Namespace: "prod", Name: "proxy", Selector: "app=proxy"},
			container: "app", digest: "sha256:new"}
		w := &workloadModel.Main{Cluster: "zeon", Namespace: "prod", Kind: "Deployment", Name: "proxy", Manifest: prev}
		s.probeManifests(context.Background(), []*workloadDraft{d}, pods, map[workloadModel.Key]*workloadModel.Main{w.Key(): w}, now)
		return d
	}

	// первый поиск — через Service, основной порт не трогается
	d := cycle(workloadModel.Manifest{}, pod("proxy-1", "sha256:new"))
	assert.Equal(t, []string{"prod/proxy:3003/.well-known/pulse"}, caller.calls)
	require.NotNil(t, d.manifestProbe)
	assert.Equal(t, workloadModel.ManifestOk, d.manifestProbe.Status)
	assert.Equal(t, "proxy", d.manifestProbe.Service)
	require.NotNil(t, d.manifest)

	// выкатка не закончилась: за Service ещё под прошлой сборки — ответить мог бы он
	accepted := *d.manifestProbe
	accepted.Digest = "sha256:old"
	d = cycle(accepted, pod("proxy-1", "sha256:old"), pod("proxy-2", "sha256:new"))
	assert.Empty(t, caller.calls)
	assert.Nil(t, d.manifestProbe, "прошлый результат остаётся")
	require.NotNil(t, d.manifest)
	assert.Empty(t, d.manifest.Commit, "коммит манифеста — только того же образа")
	// выкатка закончилась — ищем
	cycle(accepted, pod("proxy-2", "sha256:new"))
	assert.Len(t, caller.calls, 1)

	// список Service недоступен — прошлый результат
	k8s.err = errors.New("apiserver unavailable")
	d = cycle(workloadModel.Manifest{}, pod("proxy-1", "sha256:new"))
	assert.Empty(t, caller.calls)
	assert.Nil(t, d.manifestProbe)
	k8s.err = nil

	// служебного порта в Service нет — никуда не ходим, в каталоге причина
	k8s.services = []k8sModel.Service{withoutSystem}
	d = cycle(workloadModel.Manifest{}, pod("proxy-1", "sha256:new"))
	assert.Empty(t, caller.calls)
	require.NotNil(t, d.manifestProbe)
	assert.Equal(t, workloadModel.ManifestAbsent, d.manifestProbe.Status)
	assert.Contains(t, d.manifestProbe.Reasons[0], "с портом system")
	assert.Nil(t, d.manifest)
	noService := *d.manifestProbe

	// до retry_after не повторяем, но служебный порт добавили в Service — ищем сразу
	cycle(noService, pod("proxy-1", "sha256:new"))
	assert.Empty(t, caller.calls)
	k8s.services = []k8sModel.Service{withSystem}
	d = cycle(noService, pod("proxy-1", "sha256:new"))
	assert.Equal(t, []string{"prod/proxy:3003/.well-known/pulse"}, caller.calls)
	assert.Equal(t, workloadModel.ManifestOk, d.manifestProbe.Status)
}

// Манифест важнее service.yaml; манифесты нескольких workload'ов сервиса сливаются.
func TestBuildServices_Manifest(t *testing.T) {
	raw, err := os.ReadFile("model/testdata/manifest.json")
	require.NoError(t, err)
	parsed, err := localModel.ParseManifest(raw)
	require.NoError(t, err)

	second := *parsed
	second.Metadata.Endpoints = []svcModel.Endpoint{{Id: "queue", Title: "Очередь"}, {Id: "order_status"}}

	repo := "https://github.com/mechta-market/ocenter"
	yamlDraft := &workloadDraft{Workload: k8sModel.Workload{Namespace: "prod", Kind: "Deployment", Name: "ocenter"}, repoUrl: repo, serviceKey: "ocenter"}
	withManifest := &workloadDraft{Workload: k8sModel.Workload{Namespace: "prod", Kind: "Deployment", Name: "ocenter-api"}, repoUrl: repo, serviceKey: "ocenter", manifest: parsed}
	worker := &workloadDraft{Workload: k8sModel.Workload{Namespace: "prod", Kind: "Deployment", Name: "ocenter-worker"}, repoUrl: repo, serviceKey: "ocenter", manifest: &second}

	yaml, err := localModel.ParseServiceYaml([]byte("name: ocenter\ntitle: из service.yaml\n"))
	require.NoError(t, err)
	s := &Service{svc: &fakeSvc{}}
	edits := s.buildServices(context.Background(), []*workloadDraft{yamlDraft, withManifest, worker},
		map[string]metadataResult{repo: {yaml: yaml, found: true}}, time.Now())

	byName := map[string]*svcModel.Edit{}
	for _, e := range edits {
		byName[*e.Name] = e
	}
	edit := byName["orders-center"]
	require.NotNil(t, edit, "имя из манифеста")
	assert.Equal(t, "Центр заказов", *edit.Title)
	assert.Equal(t, svcModel.MetadataSourceManifest, edit.Metadata.Source)
	require.Len(t, edit.Metadata.Endpoints, 2, "ручка второго workload'а добавлена, повтор id — нет")
	assert.Equal(t, "ocenter-api", edit.Metadata.Endpoints[0].Workload.Name)
	assert.Equal(t, "ocenter-worker", edit.Metadata.Endpoints[1].Workload.Name)
	assert.Equal(t, "orders-center", withManifest.serviceKey)
}

type fakeSvc struct{ svcServiceI }

func (f *fakeSvc) List(context.Context, *svcModel.ListReq) ([]*svcModel.Main, int64, error) {
	return nil, 0, nil
}
