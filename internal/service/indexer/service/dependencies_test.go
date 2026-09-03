package service

import (
	"context"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dependencyModel "github.com/mechta-market/pulse/internal/domain/dependency/model"
	dependencyService "github.com/mechta-market/pulse/internal/domain/dependency/service"
	indexerModel "github.com/mechta-market/pulse/internal/service/indexer/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
)

type fakeK8sDeps struct {
	configMaps []k8sModel.ConfigMap
	services   []k8sModel.Service
}

func (f *fakeK8sDeps) ListWorkloads(context.Context) ([]k8sModel.Workload, error) { return nil, nil }
func (f *fakeK8sDeps) ListPods(context.Context, string, string) ([]k8sModel.Pod, error) {
	return nil, nil
}
func (f *fakeK8sDeps) ListConfigMaps(context.Context, string) ([]k8sModel.ConfigMap, error) {
	return f.configMaps, nil
}
func (f *fakeK8sDeps) ListServices(context.Context, string) ([]k8sModel.Service, error) {
	return f.services, nil
}

// fakeDepend — доменный парсер настоящий, запись перехватывается
type fakeDepend struct {
	*dependencyService.Service
	upserted []*dependencyModel.Edit
}

func (f *fakeDepend) UpdateOrCreateMany(_ context.Context, objs []*dependencyModel.Edit) error {
	f.upserted = objs
	return nil
}

func (f *fakeDepend) DeleteStale(context.Context, string, time.Time) (int64, error) { return 0, nil }

func draft(ns, name, serviceKey string, env []k8sModel.EnvVar, envFrom ...string) *workloadDraft {
	return &workloadDraft{
		Workload: k8sModel.Workload{
			Namespace: ns, Name: name, Kind: "Deployment", Selector: "app=" + name,
			Containers: []k8sModel.Container{{Name: "app", Env: env, EnvFromConfigMaps: envFrom}},
		},
		serviceKey: serviceKey,
	}
}

func TestRecordDependencies(t *testing.T) {
	k8s := &fakeK8sDeps{
		configMaps: []k8sModel.ConfigMap{{Namespace: "prod", Name: "payments-cfg", Data: map[string]string{
			"BILLING_ADDR": "billing-core.prod.svc.cluster.local:9090",
			"LOG_LEVEL":    "info",
		}}},
		services: []k8sModel.Service{
			// k8s Service с именем, отличным от workload'а: резолвится через селектор
			{Namespace: "prod", Name: "acquirer-gw", Selector: map[string]string{"app": "acquirer-gateway"}},
		},
	}
	depend := &fakeDepend{Service: dependencyService.New(nil)}
	s := &Service{conf: indexerModel.Config{Cluster: "zeon"}, k8s: k8s, depend: depend}

	drafts := []*workloadDraft{
		draft("prod", "payments-api", "payments-api", []k8sModel.EnvVar{
			{Name: "ACQUIRER_URL", Value: "http://acquirer-gw.prod.svc:8080"},
			{Name: "EPAY_URL", Value: "https://api.epay.kz/v2"},
			{Name: "REDIS_ADDR", Value: "payments-api-redis:6379"}, // свой сайдкар-редис → ссылка на себя
			{Name: "DB_DSN", FromSecret: true},                     // секрет: значение не читается
			{Name: "KAFKA_BROKERS", ConfigMapRef: "payments-cfg/BILLING_ADDR"},
			{Name: "HTTP_CORS", Value: "true"},
		}, "payments-cfg"),
		draft("prod", "acquirer-gateway", "acquirer-gateway", nil),
		draft("prod", "billing-core", "billing-core", nil),
		draft("prod", "payments-api-redis", "payments-api", nil),
	}

	count := s.recordDependencies(context.Background(), drafts, time.Now())
	assert.Equal(t, 4, count)

	byKey := lo.SliceToMap(depend.upserted, func(e *dependencyModel.Edit) (string, *dependencyModel.Edit) { return *e.Key, e })
	require.Contains(t, byKey, "ACQUIRER_URL")
	assert.Equal(t, "acquirer-gateway", *byKey["ACQUIRER_URL"].ToService, "хост k8s Service → workload через селектор")
	assert.Equal(t, int32(8080), *byKey["ACQUIRER_URL"].Port)
	assert.Equal(t, dependencyModel.SourceEnv, *byKey["ACQUIRER_URL"].Source)

	assert.Equal(t, "", *byKey["EPAY_URL"].ToService, "внешний адрес")
	assert.Equal(t, "api.epay.kz", *byKey["EPAY_URL"].ToHost)

	assert.Equal(t, "billing-core", *byKey["BILLING_ADDR"].ToService, "envFrom configmap")
	assert.Equal(t, dependencyModel.SourceConfigMap, *byKey["BILLING_ADDR"].Source)
	assert.Equal(t, "billing-core", *byKey["KAFKA_BROKERS"].ToService, "valueFrom configMapKeyRef")

	assert.NotContains(t, byKey, "REDIS_ADDR", "ссылка на себя не связь")
	assert.NotContains(t, byKey, "DB_DSN", "секрет не читается")
	assert.NotContains(t, byKey, "HTTP_CORS")
	assert.NotContains(t, byKey, "LOG_LEVEL")
}
