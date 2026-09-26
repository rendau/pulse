package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	indexerModel "github.com/mechta-market/pulse/internal/service/indexer/model"
	localModel "github.com/mechta-market/pulse/internal/service/indexer/service/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
	prometheusModel "github.com/mechta-market/pulse/internal/service/prometheus/model"
	svcproxyModel "github.com/mechta-market/pulse/internal/service/svcproxy/model"
)

var manifestConf = indexerModel.ManifestConfig{
	Path: "/.well-known/pulse", DefaultPorts: []int{3003}, AnnotationPrefix: "pulse/",
	RefreshAfter: 24 * time.Hour, RetryAfter: time.Hour, SkipPorts: []int{5432, 9092},
}

// fakePods — поды отвечают по «ip:port»: код ответа и тело; нет ключа — порт молчит.
type fakePods struct {
	answers map[string]*svcproxyModel.Response
	calls   []string
}

func (f *fakePods) GetPod(_ context.Context, t svcproxyModel.PodTarget, path string, _, headers map[string]string, _ int64) (*svcproxyModel.Response, error) {
	key := fmt.Sprintf("%s:%d", t.IP, t.Port)
	f.calls = append(f.calls, key+path)
	if headers["X-Pulse-Request-Id"] == "" {
		return nil, errors.New("no request id")
	}
	if resp, ok := f.answers[key]; ok {
		return resp, nil
	}
	return nil, errors.New("connection refused")
}

type fakeProm struct{ samples []prometheusModel.Sample }

func (f *fakeProm) Query(context.Context, string, time.Time) ([]prometheusModel.Sample, error) {
	return f.samples, nil
}

func TestCandidatePorts(t *testing.T) {
	s := &Service{conf: indexerModel.Config{Manifest: manifestConf}}
	pod := k8sModel.Pod{
		Annotations: map[string]string{"pulse/port": "8081"},
		Ports: []k8sModel.PodPort{
			{Name: "grpc", Port: 5050, Protocol: "TCP"},
			{Name: "http-api", Port: 8080, Protocol: "TCP"},
			{Name: "system", Port: 3003, Protocol: "TCP"},
			{Name: "pg", Port: 5432, Protocol: "TCP"},
			{Name: "dns", Port: 53, Protocol: "UDP"},
		},
	}

	known, rest := s.candidatePorts(pod, []int{9102}, nil)
	assert.Equal(t, []int{8081, 9102, 3003, 8080}, known, "аннотация → цель Prometheus → system → http* → по умолчанию")
	assert.Equal(t, []int{5050}, rest, "остальные TCP-порты, кроме не-HTTP и UDP")

	known, rest = s.candidatePorts(k8sModel.Pod{}, nil, nil)
	assert.Equal(t, []int{3003}, known)
	assert.Empty(t, rest)
}

// Имя system бывает только у порта k8s Service: номер — порт контейнера (targetPort числом, по
// имени или порт Service); Service чужого пода не считается.
func TestServicePorts(t *testing.T) {
	s := &Service{conf: indexerModel.Config{Manifest: manifestConf}}
	pod := k8sModel.Pod{Namespace: "prod", Labels: map[string]string{"app": "caravan", "tier": "api"},
		Ports: []k8sModel.PodPort{{Name: "metrics", Port: 9090, Protocol: "TCP"}, {Port: 8080, Protocol: "TCP"}}}
	services := []k8sModel.Service{
		{Namespace: "prod", Name: "caravan", Selector: map[string]string{"app": "caravan"}, Ports: []k8sModel.ServicePort{
			{Name: "system", Port: 80, TargetPort: "9090"},
			{Name: "http-api", Port: 81, TargetPort: "metrics"},
			{Name: "grpc", Port: 5050, TargetPort: "5050"},
		}},
		{Namespace: "prod", Name: "front", Selector: map[string]string{"app": "front"}, Ports: []k8sModel.ServicePort{{Name: "system", Port: 3003}}},
		{Namespace: "stage", Name: "caravan", Selector: map[string]string{"app": "caravan"}, Ports: []k8sModel.ServicePort{{Name: "system", Port: 4004}}},
		{Namespace: "prod", Name: "caravan-sys", Selector: map[string]string{"app": "caravan"}, Ports: []k8sModel.ServicePort{{Name: "system", Port: 3013}}},
	}

	named := servicePorts(pod, services)
	assert.Equal(t, []int32{9090, 9090, 3013}, lo.Map(named, func(p k8sModel.PodPort, _ int) int32 { return p.Port }))

	known, _ := s.candidatePorts(pod, nil, named)
	assert.Equal(t, []int{9090, 3013, 3003}, known, "system у Service → порт контейнера, затем по умолчанию")
}

func TestProbeDue(t *testing.T) {
	s := &Service{conf: indexerModel.Config{Manifest: manifestConf}}
	now := time.Now()
	probed := func(status, digest string, ago time.Duration) *workloadModel.Manifest {
		return &workloadModel.Manifest{Status: status, Digest: digest, CheckedAt: now.Add(-ago)}
	}

	assert.True(t, s.probeDue(nil, "sha256:a", now), "ещё не искали")
	assert.True(t, s.probeDue(probed("ok", "sha256:a", time.Minute), "sha256:b", now), "выкатка")
	assert.False(t, s.probeDue(probed("ok", "sha256:a", time.Hour), "sha256:a", now), "принятый — раз в сутки")
	assert.True(t, s.probeDue(probed("partial", "sha256:a", 25*time.Hour), "sha256:a", now))
	assert.False(t, s.probeDue(probed("absent", "sha256:a", 30*time.Minute), "sha256:a", now), "неудача — раз в час")
	assert.True(t, s.probeDue(probed("invalid", "sha256:a", 2*time.Hour), "sha256:a", now))
	assert.False(t, s.probeDue(probed("ok", "sha256:a", time.Hour), "", now), "digest неизвестен — не выкатка")
}

func TestProbe(t *testing.T) {
	raw, err := os.ReadFile("model/testdata/manifest.json")
	require.NoError(t, err)

	pod := k8sModel.Pod{Namespace: "prod", Name: "ocenter-1", IP: "10.0.0.5", Ready: true,
		Ports: []k8sModel.PodPort{{Name: "http", Port: 8080}, {Name: "grpc", Port: 5050}, {Port: 9090}}}

	// манифест на безымянном порту /metrics — находится по цели Prometheus
	pods := &fakePods{answers: map[string]*svcproxyModel.Response{
		"10.0.0.5:8080": {StatusCode: 404},
		"10.0.0.5:9090": {StatusCode: 200, Body: raw},
	}}
	s := &Service{conf: indexerModel.Config{Manifest: manifestConf}, pods: pods}
	result, parsed := s.probe(context.Background(), pod, nil, nil, true)
	require.NotNil(t, parsed)
	assert.Equal(t, workloadModel.ManifestOk, result.Status)
	assert.Equal(t, 9090, result.Port)
	assert.Equal(t, []string{"8080: 404", "3003: нет ответа", "5050: нет ответа", "9090: манифест"}, result.Tried)
	assert.Equal(t, raw, result.Raw)

	// без выкатки остальные порты не перебираются
	pods.calls = nil
	result, parsed = s.probe(context.Background(), pod, nil, nil, false)
	assert.Nil(t, parsed)
	assert.Equal(t, workloadModel.ManifestAbsent, result.Status, "HTTP-сервер ответил 404 — манифеста нет")
	assert.Len(t, pods.calls, 2)

	// с целью Prometheus — сразу нужный порт
	pods.calls = nil
	result, _ = s.probe(context.Background(), pod, []int{9090}, nil, false)
	assert.Equal(t, workloadModel.ManifestOk, result.Status)
	assert.Equal(t, []string{"10.0.0.5:9090/.well-known/pulse"}, pods.calls)

	// 200 на любой путь (SPA) — не манифест
	pods.answers["10.0.0.5:8080"] = &svcproxyModel.Response{StatusCode: 200, Body: []byte("<!doctype html>")}
	pods.answers["10.0.0.5:9090"] = &svcproxyModel.Response{StatusCode: 200, Body: []byte(`{"status":"ok"}`)}
	result, parsed = s.probe(context.Background(), pod, nil, nil, true)
	assert.Nil(t, parsed)
	assert.Equal(t, workloadModel.ManifestAbsent, result.Status)
	assert.Contains(t, result.Tried, "8080: 200, не манифест")

	// большая страница на любой путь (обрезана по лимиту) — тоже не манифест
	pods.answers["10.0.0.5:9090"] = &svcproxyModel.Response{StatusCode: 200, Body: []byte("<html>…"), Truncated: true}
	result, _ = s.probe(context.Background(), pod, []int{9090}, nil, false)
	assert.Equal(t, workloadModel.ManifestAbsent, result.Status)
	// а обрезанный манифест — invalid
	pods.answers["10.0.0.5:9090"] = &svcproxyModel.Response{StatusCode: 200, Body: []byte(`{"pulse_manifest": 1, "service": {`), Truncated: true}
	result, _ = s.probe(context.Background(), pod, []int{9090}, nil, false)
	assert.Equal(t, workloadModel.ManifestInvalid, result.Status)

	// невалидный манифест
	pods.answers["10.0.0.5:9090"] = &svcproxyModel.Response{StatusCode: 200, Body: []byte(`{"pulse_manifest":1}`)}
	result, parsed = s.probe(context.Background(), pod, []int{9090}, nil, false)
	assert.Nil(t, parsed)
	assert.Equal(t, workloadModel.ManifestInvalid, result.Status)
	assert.NotEmpty(t, result.Reasons)
	assert.Empty(t, result.Raw)

	// никто не ответил
	result, _ = s.probe(context.Background(), k8sModel.Pod{IP: "10.0.0.9"}, nil, nil, true)
	assert.Equal(t, workloadModel.ManifestUnreachable, result.Status)
}

func TestScrapePorts(t *testing.T) {
	s := &Service{prom: &fakeProm{samples: []prometheusModel.Sample{
		{Labels: map[string]string{"namespace": "prod", "pod": "ocenter-1", "instance": "10.0.0.5:9090"}},
		{Labels: map[string]string{"namespace": "prod", "pod": "ocenter-1", "instance": "10.0.0.5:3003"}},
		{Labels: map[string]string{"namespace": "prod", "pod": "x", "instance": "bad"}},
	}}}
	assert.Equal(t, map[string][]int{"prod/ocenter-1": {3003, 9090}}, s.scrapePorts(context.Background()))
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
