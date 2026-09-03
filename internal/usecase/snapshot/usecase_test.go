package snapshot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse/internal/constant"
	eventService "github.com/mechta-market/pulse/internal/domain/event/service"
	snapshotModel "github.com/mechta-market/pulse/internal/domain/snapshot/model"
	snapshotService "github.com/mechta-market/pulse/internal/domain/snapshot/service"
	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	alertmanagerModel "github.com/mechta-market/pulse/internal/service/alertmanager/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
	prometheusModel "github.com/mechta-market/pulse/internal/service/prometheus/model"
	"github.com/mechta-market/pulse/internal/usecase/snapshot/model"
)

// fakes

type fakeSvc struct{ service *svcModel.Main }

func (f *fakeSvc) GetOrSuggest(context.Context, string) (*svcModel.Main, error) {
	return f.service, nil
}

type fakeWorkload struct{ items []*workloadModel.Main }

func (f *fakeWorkload) List(context.Context, *workloadModel.ListReq) ([]*workloadModel.Main, int64, error) {
	return f.items, int64(len(f.items)), nil
}

type fakeK8s struct {
	pods   []k8sModel.Pod
	events []k8sModel.Event
	err    error
}

func (f *fakeK8s) ListPods(context.Context, string, string) ([]k8sModel.Pod, error) {
	return f.pods, f.err
}

func (f *fakeK8s) ListEvents(context.Context, string, time.Time) ([]k8sModel.Event, error) {
	return f.events, f.err
}

type fakePrometheus struct {
	values map[string]float64 // promql → значение сейчас; час назад ×0.9, вчера ×0.5
	err    error
}

func (f *fakePrometheus) Query(_ context.Context, promql string, at time.Time) ([]prometheusModel.Sample, error) {
	if f.err != nil {
		return nil, f.err
	}
	v, ok := f.values[promql]
	if !ok {
		return nil, nil
	}
	switch {
	case time.Since(at) > 23*time.Hour:
		v *= 0.5
	case time.Since(at) > 50*time.Minute:
		v *= 0.9
	}
	return []prometheusModel.Sample{{Value: v, TS: at}}, nil
}

func (f *fakePrometheus) QueryRange(_ context.Context, _ string, start, end time.Time, step time.Duration) ([]prometheusModel.Series, error) {
	if f.err != nil {
		return nil, f.err
	}
	points := make([]prometheusModel.Point, 0)
	for ts := start; !ts.After(end); ts = ts.Add(step) {
		points = append(points, prometheusModel.Point{TS: ts, Value: 1})
	}
	return []prometheusModel.Series{{Labels: map[string]string{"job": "a"}, Points: points}}, nil
}

type fakeAlertmanager struct {
	alerts []alertmanagerModel.Alert
	err    error
}

func (f *fakeAlertmanager) ListAlerts(context.Context) ([]alertmanagerModel.Alert, error) {
	return f.alerts, f.err
}

// fixtures

const promRPS = `sum(rate(request_total{namespace="prod", pod=~"^(payments-api)-.*"}[5m]))`

func fixtures() (*fakeSvc, *fakeWorkload) {
	svc := &fakeSvc{service: &svcModel.Main{Name: "payments-api", Title: "Платежи"}}
	wl := &fakeWorkload{items: []*workloadModel.Main{{
		Cluster: "zeon", Namespace: "prod", Kind: constant.WorkloadKindDeployment, Name: "payments-api",
		ReplicasDesired: 3, Image: "ghcr.io/org/payments-api:latest", Selector: "app=payments-api",
	}}}
	return svc, wl
}

func newUsecase(k8s *fakeK8s, prom PrometheusI, am AlertmanagerI) *Usecase {
	svc, wl := fixtures()
	return New(Config{
		Deadline: 2 * time.Second, MaxEvents: 50, MaxAlerts: 50,
		DefaultMetrics: []snapshotModel.MetricDef{{Id: "rps", PromQL: `sum(rate(request_total{namespace="{namespace}", pod=~"{pod_regex}"}[5m]))`}},
		MaxWindow:      7 * 24 * time.Hour, MaxSeries: 20, MaxPoints: 200,
	}, svc, wl, k8s, prom, am, nil, eventService.New(), snapshotService.New(snapshotService.Config{AnomalyThresholdPct: 30}))
}

func TestSnapshot_Degraded(t *testing.T) {
	now := time.Now()
	k8s := &fakeK8s{
		pods: []k8sModel.Pod{
			{Name: "payments-api-1", Ready: true, Phase: "Running", StartedAt: now.Add(-2 * time.Hour)},
			{Name: "payments-api-2", Ready: false, Phase: "Running", Restarts: 7, Containers: []k8sModel.PodContainer{{
				Name: "app", State: "waiting", Reason: "CrashLoopBackOff", Restarts: 7,
				LastTerminationReason: "OOMKilled", LastTerminatedAt: now.Add(-10 * time.Minute),
			}}},
		},
		events: []k8sModel.Event{
			{Namespace: "prod", ObjectKind: "Pod", ObjectName: "payments-api-2", Reason: "BackOff", Type: "Warning", Message: "Back-off restarting failed container", Count: 12, LastTS: now.Add(-5 * time.Minute)},
			{Namespace: "prod", ObjectKind: "Pod", ObjectName: "other-svc-1", Reason: "BackOff", Type: "Warning", LastTS: now},
			{Namespace: "prod", ObjectKind: "Pod", ObjectName: "payments-api-1", Reason: "Pulled", Type: "Normal", LastTS: now},
		},
	}
	prom := &fakePrometheus{values: map[string]float64{promRPS: 100}}
	am := &fakeAlertmanager{alerts: []alertmanagerModel.Alert{
		{Labels: map[string]string{"alertname": "HighErrorRate", "severity": "critical", "service": "payments-api"}, Annotations: map[string]string{"summary": "5xx > 5%"}, StartsAt: now.Add(-20 * time.Minute), State: "active"},
		{Labels: map[string]string{"alertname": "Other", "severity": "critical", "service": "delivery"}, StartsAt: now, State: "active"},
	}}

	snap, err := newUsecase(k8s, prom, am).Snapshot(context.Background(), "payments-api", time.Hour)
	require.NoError(t, err)

	assert.Equal(t, snapshotModel.HealthDegraded, snap.Health)
	sources := lo.Map(snap.Errors, func(e snapshotModel.SourceError, _ int) string { return e.Source })
	assert.Equal(t, []string{constant.SourceLoki}, sources, "loki не сконфигурирован — единственная ошибка")

	require.Len(t, snap.Alerts, 1, "чужой алерт отфильтрован")
	assert.Equal(t, "HighErrorRate", snap.Alerts[0].Name)

	require.Len(t, snap.Workloads, 1)
	assert.Equal(t, 1, snap.Workloads[0].Pods.Ready)
	assert.Equal(t, 2, snap.Workloads[0].Pods.Total)
	assert.Equal(t, int32(7), snap.Workloads[0].Pods.Restarts)
	require.NotEmpty(t, snap.Workloads[0].Pods.Problems)
	assert.Equal(t, "CrashLoopBackOff", snap.Workloads[0].Pods.Problems[0].Reason)

	require.Len(t, snap.Metrics, 1)
	rps := snap.Metrics[0]
	assert.Equal(t, "rps", rps.Id)
	assert.Equal(t, promRPS, rps.PromQL, "плейсхолдеры подставлены")
	assert.Equal(t, 100.0, *rps.Current)
	assert.Equal(t, 90.0, *rps.HourAgo)
	assert.Equal(t, 50.0, *rps.SameTimeYesterday)
	assert.Equal(t, 100.0, *rps.DeltaVsYesterday)
	assert.True(t, rps.Anomaly)

	types := lo.Map(snap.RecentEvents, func(e eventModelEvent, _ int) string { return e.Type })
	assert.Contains(t, types, constant.EventTypeAlertFiring)
	assert.Contains(t, types, constant.EventTypeOOMKill, "OOM из статуса контейнера")
	assert.Contains(t, types, constant.EventTypeRestart, "BackOff из событий")
	assert.NotContains(t, types, constant.EventTypeInfo, "Normal-события отброшены")
	for _, e := range snap.RecentEvents {
		assert.NotContains(t, e.Summary, "other-svc", "чужие события отфильтрованы")
	}
	for i := 1; i < len(snap.RecentEvents); i++ {
		assert.False(t, snap.RecentEvents[i].TS.After(snap.RecentEvents[i-1].TS), "события отсортированы по убыванию времени")
	}

	assert.NotEmpty(t, snap.SummaryHints)
	t.Logf("hints: %v", snap.SummaryHints)
}

func TestSnapshot_PartialWhenSourcesFail(t *testing.T) {
	k8s := &fakeK8s{pods: []k8sModel.Pod{{Name: "payments-api-1", Ready: true, Phase: "Running"}}}
	prom := &fakePrometheus{err: errors.New("connection refused")}
	am := &fakeAlertmanager{err: errors.New("503")}

	snap, err := newUsecase(k8s, prom, am).Snapshot(context.Background(), "payments-api", time.Hour)
	require.NoError(t, err)

	sources := lo.Map(snap.Errors, func(e snapshotModel.SourceError, _ int) string { return e.Source })
	assert.ElementsMatch(t, []string{constant.SourcePrometheus, constant.SourceAlertmanager, constant.SourceLoki}, sources)
	assert.Equal(t, 1, snap.Workloads[0].Pods.Ready, "k8s-часть на месте")
	require.Len(t, snap.Metrics, 1)
	assert.NotEmpty(t, snap.Metrics[0].Error)
	assert.Equal(t, snapshotModel.HealthDegraded, snap.Health, "1 из 3 подов готов")
}

func TestSnapshot_UnknownWhenK8sDown(t *testing.T) {
	k8s := &fakeK8s{err: errors.New("dial tcp: i/o timeout")}

	snap, err := newUsecase(k8s, nil, nil).Snapshot(context.Background(), "payments-api", time.Hour)
	require.NoError(t, err)

	assert.Equal(t, snapshotModel.HealthUnknown, snap.Health)
	sources := lo.Map(snap.Errors, func(e snapshotModel.SourceError, _ int) string { return e.Source })
	assert.ElementsMatch(t, []string{constant.SourceK8s, constant.SourcePrometheus, constant.SourceLoki}, sources, "несконфигурированные источники — тоже в errors")
}

func TestQueryMetrics(t *testing.T) {
	prom := &fakePrometheus{}
	u := newUsecase(&fakeK8s{}, prom, nil)
	ctx := context.Background()

	t.Run("by metric id with placeholders", func(t *testing.T) {
		res, err := u.QueryMetrics(ctx, &model.QueryMetricsReq{Service: "payments-api", MetricId: "rps", Window: time.Hour})
		require.NoError(t, err)
		assert.Equal(t, promRPS, res.Def.PromQL)
		assert.Equal(t, 36*time.Second, res.Step, "window/100, округлено до секунд")
		require.Len(t, res.Series, 1)
		assert.LessOrEqual(t, len(res.Series[0].Points), 200)
	})

	t.Run("unknown metric id lists available", func(t *testing.T) {
		_, err := u.QueryMetrics(ctx, &model.QueryMetricsReq{Service: "payments-api", MetricId: "nope"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "available: rps")
	})

	t.Run("too many points", func(t *testing.T) {
		_, err := u.QueryMetrics(ctx, &model.QueryMetricsReq{Service: "payments-api", PromQL: "up", Window: 24 * time.Hour, Step: 15 * time.Second})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "increase step")
	})

	t.Run("window too large", func(t *testing.T) {
		_, err := u.QueryMetrics(ctx, &model.QueryMetricsReq{Service: "payments-api", PromQL: "up", Window: 30 * 24 * time.Hour})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds maximum")
	})

	t.Run("prometheus not configured", func(t *testing.T) {
		_, err := newUsecase(&fakeK8s{}, nil, nil).QueryMetrics(ctx, &model.QueryMetricsReq{Service: "payments-api", PromQL: "up"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "PROMETHEUS_URL")
	})
}
