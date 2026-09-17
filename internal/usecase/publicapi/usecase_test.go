package publicapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse/internal/constant"
	dependencyModel "github.com/mechta-market/pulse/internal/domain/dependency/model"
	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	"github.com/mechta-market/pulse/internal/errs"
	prometheusModel "github.com/mechta-market/pulse/internal/service/prometheus/model"
	rutoModel "github.com/mechta-market/pulse/internal/service/ruto/model"
)

type fakeSvc struct{}

func (fakeSvc) GetOrSuggest(_ context.Context, name string) (*svcModel.Main, error) {
	return &svcModel.Main{Name: name}, nil
}

type fakeDepend struct{ items []*dependencyModel.Main }

func (f *fakeDepend) List(_ context.Context, pars *dependencyModel.ListReq) ([]*dependencyModel.Main, int64, error) {
	items := lo.Filter(f.items, func(d *dependencyModel.Main, _ int) bool { return lo.Contains(pars.ToServices, d.ToService) })
	return items, int64(len(items)), nil
}

type fakeRuto struct {
	snapshot *rutoModel.Snapshot
	err      error
}

func (f *fakeRuto) GetSnapshot(context.Context) (*rutoModel.Snapshot, error) {
	return f.snapshot, f.err
}

type fakePrometheus struct{ queries []string }

func (f *fakePrometheus) Query(_ context.Context, promql string, _ time.Time) ([]prometheusModel.Sample, error) {
	f.queries = append(f.queries, promql)
	sample := func(value float64, labels ...string) prometheusModel.Sample {
		s := prometheusModel.Sample{Labels: map[string]string{}, Value: value}
		for i := 0; i+1 < len(labels); i += 2 {
			s.Labels[labels[i]] = labels[i+1]
		}
		return s
	}
	switch {
	case strings.HasPrefix(promql, "sum by (app, method, status)"):
		return []prometheusModel.Sample{
			sample(3000, "app", "ocenter", "method", "GET /ocenter/order/{id}", "status", "200"),
			sample(360, "app", "ocenter", "method", "GET /ocenter/order/{id}", "status", "502"),
			sample(240, "app", "ocenter", "method", "GET /ocenter/order/{id}", "status", "404"),
			sample(36, "app", "ocenter", "method", "POST /ocenter/old", "status", "200"),
		}, nil
	case strings.Contains(promql, "sum by (method, le)"):
		return []prometheusModel.Sample{sample(0.42, "method", "GET /ocenter/order/{id}")}, nil
	default:
		return []prometheusModel.Sample{sample(0.5)}, nil
	}
}

func fixtures() (*fakeDepend, *fakeRuto) {
	depend := &fakeDepend{items: []*dependencyModel.Main{
		{FromService: "ruto-gateway", ToService: "ocenter", Source: dependencyModel.SourceRuto, Key: "ocenter"},
		{FromService: "cart", ToService: "ocenter", Source: dependencyModel.SourceEnv, Key: "OCENTER_URL"},
	}}
	ruto := &fakeRuto{snapshot: &rutoModel.Snapshot{BaseUrl: "https://api.mdev.kz", Apps: []rutoModel.App{
		{Name: "ocenter", Active: true, PathPrefix: "/ocenter", BackendUrl: "http://ocenter.default.svc", Endpoints: []rutoModel.Endpoint{
			{Active: true, Type: "http", Method: "GET", Path: "order/{id}"},
			{Active: true, Type: "http", Method: "POST", Path: "order"},
			{Active: false, Type: "http", Method: "DELETE", Path: "order/{id}"},
		}},
		{Name: "cart", Active: true, PathPrefix: "/cart", Endpoints: []rutoModel.Endpoint{{Active: true, Method: "GET", Path: "x"}}},
	}}}
	return depend, ruto
}

func TestPublicApi(t *testing.T) {
	depend, ruto := fixtures()
	prom := &fakePrometheus{}
	u := New(Config{RequestsMetric: "gw_requests_total", DurationMetric: "gw_duration_seconds"}, fakeSvc{}, depend, ruto, prom)

	res, err := u.PublicApi(context.Background(), "ocenter", time.Hour)
	require.NoError(t, err)
	assert.Empty(t, res.Errors)
	assert.Equal(t, "https://api.mdev.kz", res.BaseUrl)
	require.Len(t, res.Apps, 1, "только приложения, ведущие на сервис")
	assert.Equal(t, 2, res.Apps[0].Endpoints)
	assert.Equal(t, 1, res.Apps[0].InactiveEndpoints)

	for _, q := range prom.queries {
		assert.Contains(t, q, `{app=~"ocenter"}[3600s]`)
	}

	require.NotNil(t, res.Traffic)
	assert.Equal(t, 3636.0, res.Traffic.Requests)
	assert.Equal(t, 1.01, res.Traffic.Rps)
	assert.InDelta(t, 360.0/3636, *res.Traffic.ErrorRate, 0.001, "ошибки — только 5xx, 404 не ошибка сервера")
	assert.Equal(t, "200", res.Traffic.Statuses[0].Status)

	require.Len(t, res.Routes, 3)
	top := res.Routes[0]
	assert.Equal(t, "GET /ocenter/order/{id}", top.Route)
	assert.True(t, top.Configured)
	assert.Equal(t, 360.0, top.Errors)
	assert.Equal(t, 0.1, *top.ErrorRate)
	assert.Equal(t, 0.42, *top.P95)

	legacy := res.Routes[1]
	assert.Equal(t, "POST /ocenter/old", legacy.Route)
	assert.False(t, legacy.Configured, "трафик есть, маршрута в конфигурации нет")

	assert.Equal(t, "POST /ocenter/order", res.Routes[2].Route)
	assert.Zero(t, res.Routes[2].Requests)
}

func TestPublicApi_NotPublishedAndSourceErrors(t *testing.T) {
	depend, ruto := fixtures()

	_, err := New(Config{}, fakeSvc{}, depend, ruto, nil).PublicApi(context.Background(), "cart", time.Hour)
	assert.ErrorContains(t, err, "not published through ruto")

	_, err = New(Config{}, fakeSvc{}, depend, nil, nil).PublicApi(context.Background(), "ocenter", time.Hour)
	assert.ErrorIs(t, err, errs.ServiceNA)

	ruto.err = errors.New("connection refused")
	res, err := New(Config{}, fakeSvc{}, depend, ruto, nil).PublicApi(context.Background(), "ocenter", time.Hour)
	require.NoError(t, err, "частичный результат вместо отказа")
	assert.ElementsMatch(t, []string{constant.SourceRuto, constant.SourcePrometheus},
		[]string{res.Errors[0].Source, res.Errors[1].Source})
	assert.Nil(t, res.Traffic)
}
