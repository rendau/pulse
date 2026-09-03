package timeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse/internal/constant"
	deployModel "github.com/mechta-market/pulse/internal/domain/deploy/model"
	eventService "github.com/mechta-market/pulse/internal/domain/event/service"
	snapshotService "github.com/mechta-market/pulse/internal/domain/snapshot/service"
	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	githubModel "github.com/mechta-market/pulse/internal/service/github/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
	kusecModel "github.com/mechta-market/pulse/internal/service/kusec/model"
	prometheusModel "github.com/mechta-market/pulse/internal/service/prometheus/model"
	"github.com/mechta-market/pulse/internal/usecase/timeline/model"
)

// fakes

type fakeSvc struct{ services []*svcModel.Main }

func (f *fakeSvc) GetOrSuggest(_ context.Context, name string) (*svcModel.Main, error) {
	if s, ok := lo.Find(f.services, func(s *svcModel.Main) bool { return s.Name == name }); ok {
		return s, nil
	}
	return nil, errors.New("unknown service " + name)
}

func (f *fakeSvc) List(context.Context, *svcModel.ListReq) ([]*svcModel.Main, int64, error) {
	return f.services, int64(len(f.services)), nil
}

type fakeWorkload struct{ items []*workloadModel.Main }

func (f *fakeWorkload) List(_ context.Context, pars *workloadModel.ListReq) ([]*workloadModel.Main, int64, error) {
	items := lo.Filter(f.items, func(w *workloadModel.Main, _ int) bool {
		if pars.ServiceName != nil {
			return w.ServiceName == *pars.ServiceName
		}
		return len(pars.ServiceNames) == 0 || lo.Contains(pars.ServiceNames, w.ServiceName)
	})
	return items, int64(len(items)), nil
}

type fakeDeploy struct{ items []*deployModel.Main }

func (f *fakeDeploy) List(context.Context, *deployModel.ListReq) ([]*deployModel.Main, int64, error) {
	return f.items, int64(len(f.items)), nil
}

type fakeK8s struct {
	pods   []k8sModel.Pod
	events []k8sModel.Event
}

func (f *fakeK8s) ListPods(context.Context, string, string) ([]k8sModel.Pod, error) {
	return f.pods, nil
}
func (f *fakeK8s) ListEvents(context.Context, string, time.Time) ([]k8sModel.Event, error) {
	return f.events, nil
}

type fakeGithub struct {
	commits []githubModel.Commit
	cmp     *githubModel.Comparison
	err     error
}

func (f *fakeGithub) ListCommits(context.Context, string, time.Time, time.Time, int) ([]githubModel.Commit, error) {
	return f.commits, f.err
}

func (f *fakeGithub) CompareCommits(context.Context, string, string) (*githubModel.Comparison, error) {
	return f.cmp, f.err
}

type fakeKusec struct{ changes []kusecModel.Change }

func (f *fakeKusec) ListChanges(context.Context, string, time.Time, time.Time) ([]kusecModel.Change, error) {
	return f.changes, nil
}

type fakePrometheus struct{ series []prometheusModel.Series }

func (f *fakePrometheus) QueryRange(context.Context, string, time.Time, time.Time, time.Duration) ([]prometheusModel.Series, error) {
	return f.series, nil
}

// fixtures

func newUsecase(now time.Time, gh githubClientI, kusec KusecI, prom PrometheusI) *Usecase {
	svc := &fakeSvc{services: []*svcModel.Main{
		{Name: "payments-api", RepoUrl: "https://github.com/org/payments-api"},
		{Name: "delivery", RepoUrl: "https://github.com/org/delivery"},
	}}
	wl := &fakeWorkload{items: []*workloadModel.Main{
		{Cluster: "zeon", Namespace: "prod", Kind: "Deployment", Name: "payments-api", ServiceName: "payments-api", Selector: "app=payments-api", DeployedCommit: "a3f9c21b7e1d02a3f9c21b7e1d02a3f9c21b7e1d"},
		{Cluster: "zeon", Namespace: "prod", Kind: "Deployment", Name: "delivery", ServiceName: "delivery", Selector: "app=delivery"},
	}}
	deploy := &fakeDeploy{items: []*deployModel.Main{{
		Cluster: "zeon", Namespace: "prod", Kind: "Deployment", Name: "payments-api", ServiceName: "payments-api",
		Image: "ghcr.io/org/payments-api:latest", ImageDigest: "sha256:bbbb", DeployedCommit: "a3f9c21b7e1d02a3f9c21b7e1d02a3f9c21b7e1d",
		PrevImageDigest: "sha256:aaaa", PrevCommit: "0000000111", ObservedAt: now.Add(-24 * time.Minute),
	}}}
	k8s := &fakeK8s{
		events: []k8sModel.Event{
			{Namespace: "prod", ObjectKind: "ReplicaSet", ObjectName: "payments-api-7d9f", Reason: "ScalingReplicaSet", Type: "Normal", Message: "Scaled up replica set to 6", LastTS: now.Add(-25 * time.Minute)},
			{Namespace: "prod", ObjectKind: "Pod", ObjectName: "payments-api-7d9f-x1", Reason: "BackOff", Type: "Warning", Message: "Back-off restarting failed container", Count: 5, LastTS: now.Add(-10 * time.Minute)},
			{Namespace: "prod", ObjectKind: "Pod", ObjectName: "unrelated-1", Reason: "BackOff", Type: "Warning", LastTS: now},
		},
		pods: []k8sModel.Pod{{Name: "payments-api-7d9f-x1", Containers: []k8sModel.PodContainer{{Name: "app", LastTerminationReason: "OOMKilled", LastTerminatedAt: now.Add(-5 * time.Minute), Restarts: 3}}}},
	}
	return New(Config{Deadline: 2 * time.Second, MaxEvents: 100, CommitsLimit: 50}, svc, wl, deploy, k8s, gh, kusec, prom,
		eventService.New(), snapshotService.New(snapshotService.Config{}))
}

func TestTimeline_DeployBeforeErrors(t *testing.T) {
	now := time.Now().UTC()
	gh := &fakeGithub{commits: []githubModel.Commit{{SHA: "a3f9c21b7e1d02", Author: "i.petrov", Message: "fix acquirer retry", Date: now.Add(-40 * time.Minute)}}}
	kusec := &fakeKusec{changes: []kusecModel.Change{
		{TS: now.Add(-50 * time.Minute), Kind: kusecModel.KindEnv, Key: "ACQUIRER_URL", OldValue: "http://old.svc:8080", NewValue: "http://acquirer-gateway.prod.svc:8080", Author: "ops"},
		{TS: now.Add(-49 * time.Minute), Kind: kusecModel.KindEnv, Key: "DB_PASSWORD", OldValue: "old-secret", NewValue: "new-secret"},
		{TS: now.Add(-48 * time.Minute), Kind: kusecModel.KindSecret, Key: "acquirer-key", OldValue: "k1", NewValue: "k2"},
	}}
	prom := &fakePrometheus{series: []prometheusModel.Series{{
		Labels: map[string]string{"alertname": "HighErrorRate", "severity": "critical", "service": "payments-api"},
		// точки с шагом в минуту (как отдаёт range-запрос): -20m … -15m
		Points: lo.Map(lo.Range(6), func(i int, _ int) prometheusModel.Point {
			return prometheusModel.Point{TS: now.Add(time.Duration(-20+i) * time.Minute), Value: 1}
		}),
	}, {
		Labels: map[string]string{"alertname": "Other", "service": "someone-else"},
		Points: []prometheusModel.Point{{TS: now, Value: 1}},
	}}}

	res, err := newUsecase(now, gh, kusec, prom).Timeline(context.Background(), &model.TimelineReq{Services: []string{"payments-api"}, Window: time.Hour})
	require.NoError(t, err)
	assert.Empty(t, res.Errors)
	assert.False(t, res.Truncated)

	types := lo.Map(res.Events, func(e eventModel_Event, _ int) string { return e.Type })
	assert.ElementsMatch(t, []string{
		constant.EventTypeDeploy, constant.EventTypeCommit, constant.EventTypeConfigChange, constant.EventTypeConfigChange, constant.EventTypeConfigChange,
		constant.EventTypeAlertFiring, constant.EventTypeScale, constant.EventTypeRestart, constant.EventTypeOOMKill,
	}, types)

	for i := 1; i < len(res.Events); i++ {
		assert.False(t, res.Events[i].TS.After(res.Events[i-1].TS), "по убыванию времени")
	}

	// критерий фазы 4: деплой раньше первого error-события, с корректной дистанцией
	deploy, _ := lo.Find(res.Events, func(e eventModel_Event) bool { return e.Type == constant.EventTypeDeploy })
	firstError, _ := lo.Find(res.Events, func(e eventModel_Event) bool { return e.Type == constant.EventTypeAlertFiring })
	assert.True(t, deploy.TS.Before(firstError.TS))
	assert.Equal(t, 4*time.Minute, firstError.TS.Sub(deploy.TS).Round(time.Minute))
	assert.Contains(t, deploy.Summary, "0000000 → a3f9c21")

	// секреты не протекают в события конфигурации (ТЗ 4.2)
	for _, e := range res.Events {
		if e.Type != constant.EventTypeConfigChange {
			continue
		}
		assert.NotContains(t, e.Summary, "old-secret")
		assert.NotContains(t, e.Summary, "new-secret")
		assert.NotContains(t, e.Summary, "k1")
		assert.NotContains(t, e.Summary, "k2")
		if e.Details["key"] == "ACQUIRER_URL" {
			assert.Contains(t, e.Summary, "http://acquirer-gateway.prod.svc:8080")
		}
	}

	alert, _ := lo.Find(res.Events, func(e eventModel_Event) bool { return e.Type == constant.EventTypeAlertFiring })
	assert.Equal(t, constant.SeverityCritical, alert.Severity)
	assert.Equal(t, "payments-api", alert.Service)
	assert.Contains(t, alert.Summary, "погас через 5m")
}

func TestTimeline_ClusterScopeAndErrors(t *testing.T) {
	now := time.Now().UTC()
	u := newUsecase(now, &fakeGithub{err: errors.New("rate limited")}, nil, nil)

	res, err := u.Timeline(context.Background(), &model.TimelineReq{Scope: model.ScopeCluster, Window: time.Hour})
	require.NoError(t, err)
	assert.Equal(t, []string{"payments-api", "delivery"}, res.Services)
	sources := lo.Map(res.Errors, func(e model.SourceError, _ int) string { return e.Source })
	assert.ElementsMatch(t, []string{constant.SourcePrometheus}, sources, "в scope=cluster коммиты и kusec не запрашиваются")
	assert.NotEmpty(t, res.Events)

	_, err = u.Timeline(context.Background(), &model.TimelineReq{})
	assert.ErrorContains(t, err, "required")

	_, err = u.Timeline(context.Background(), &model.TimelineReq{Scope: "team"})
	assert.ErrorContains(t, err, "scope")
}

func TestChanges_Unreleased(t *testing.T) {
	now := time.Now().UTC()
	gh := &fakeGithub{
		commits: []githubModel.Commit{{SHA: "c2", Author: "a", Message: "two", Date: now.Add(-5 * time.Minute)}},
		cmp:     &githubModel.Comparison{AheadBy: 2, Commits: []githubModel.Commit{{SHA: "c2"}, {SHA: "c1"}}},
	}
	kusec := &fakeKusec{changes: []kusecModel.Change{{TS: now, Kind: kusecModel.KindConfigMap, Key: "PG_DSN", OldValue: "postgres://u:p@h/db", NewValue: "postgres://u:p2@h/db"}}}

	res, err := newUsecase(now, gh, kusec, nil).Changes(context.Background(), "payments-api", time.Hour)
	require.NoError(t, err)
	assert.Empty(t, res.Errors)
	require.NotNil(t, res.Unreleased)
	assert.Equal(t, 2, res.Unreleased.BehindBy)
	assert.Len(t, res.Unreleased.Commits, 2)
	assert.Len(t, res.Commits, 1)
	assert.Len(t, res.Deploys, 1)
	require.Len(t, res.ConfigChanges, 1)
	assert.Equal(t, "***", res.ConfigChanges[0].NewValue, "DSN маскируется по имени ключа")

	// сервис без задеплоенного коммита: unreleased отсутствует, kusec не подключён — в errors
	res, err = newUsecase(now, gh, nil, nil).Changes(context.Background(), "delivery", time.Hour)
	require.NoError(t, err)
	assert.Nil(t, res.Unreleased)
	assert.Equal(t, constant.SourceKusec, res.Errors[0].Source)
}
