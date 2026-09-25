package timeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
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
	pods        []k8sModel.Pod
	events      []k8sModel.Event
	replicaSets []k8sModel.ReplicaSet

	mu       sync.Mutex
	podCalls []string // "namespace|selector"
}

func (f *fakeK8s) ListPods(_ context.Context, namespace, selector string) ([]k8sModel.Pod, error) {
	f.mu.Lock()
	f.podCalls = append(f.podCalls, namespace+"|"+selector)
	f.mu.Unlock()
	return f.pods, nil
}

func (f *fakeK8s) ListReplicaSets(context.Context, string, string) ([]k8sModel.ReplicaSet, error) {
	return f.replicaSets, nil
}

func (f *fakeK8s) ListEvents(context.Context, string, time.Time) ([]k8sModel.Event, error) {
	return f.events, nil
}

type fakeGithub struct {
	commits []githubModel.Commit
	// older — коммиты вне окна: отдаются только запросу без since («последний коммит»)
	older []githubModel.Commit
	cmp   *githubModel.Comparison
	err   error
}

func (f *fakeGithub) ListCommits(_ context.Context, _ string, since, _ time.Time, _ int) ([]githubModel.Commit, error) {
	if since.IsZero() && len(f.older) > 0 {
		return f.older, f.err
	}
	return f.commits, f.err
}

func (f *fakeGithub) CompareCommits(context.Context, string, string) (*githubModel.Comparison, error) {
	return f.cmp, f.err
}

// fakeKusec — объекты kusec: kube_name → app id; аудит, sync и drift отдаются как есть.
type fakeKusec struct {
	apps  map[string]string
	audit []kusecModel.AuditEntry
	runs  []kusecModel.SyncRun
	drift *kusecModel.Drift
}

func (f *fakeKusec) Resolve(_ context.Context, namespace, kubeName string) (*kusecModel.Resolved, error) {
	id, ok := f.apps[kubeName]
	if !ok {
		return &kusecModel.Resolved{}, nil
	}
	return &kusecModel.Resolved{Found: true, AppId: id, Namespace: namespace}, nil
}

func (f *fakeKusec) ListAudit(context.Context, *kusecModel.AuditReq) ([]kusecModel.AuditEntry, error) {
	return f.audit, nil
}

func (f *fakeKusec) ListSyncRuns(context.Context, *kusecModel.SyncRunReq) ([]kusecModel.SyncRun, error) {
	return f.runs, nil
}

func (f *fakeKusec) GetDrift(context.Context, string) (*kusecModel.Drift, error) {
	return f.drift, nil
}

// configItemUpdate — запись аудита об изменении значения ключа.
func configItemUpdate(ts time.Time, kind, key, oldValue, newValue string) kusecModel.AuditEntry {
	entry := kusecModel.AuditEntry{
		CreatedAt: ts, ActorName: "ops", Source: "ui", Action: "update", Key: key,
		KubeKind: kind, KubeName: "kusec-payments-api-main", EntityType: kusecModel.EntityConfigItem,
	}
	change := kusecModel.AuditChange{Field: kusecModel.ValueField, Old: &oldValue, New: &newValue}
	if kind == kusecModel.KubeKindSecret {
		entry.EntityType = kusecModel.EntityItem
		change = kusecModel.AuditChange{Field: kusecModel.ValueField, OldHash: "a1", NewHash: "b2", OldSize: new(int64(2)), NewSize: new(int64(2))}
	}
	entry.Changes = []kusecModel.AuditChange{change, {Field: "description", Old: new("x"), New: new("y")}}
	return entry
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
		{Cluster: "zeon", Namespace: "prod", Kind: "Deployment", Name: "payments-api", ServiceName: "payments-api", Selector: "app=payments-api",
			ConfigRefs: []string{"kube-root-ca.crt", "kusec-payments-api-main"}, DeployedCommit: "a3f9c21b7e1d02a3f9c21b7e1d02a3f9c21b7e1d"},
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
	kusec := &fakeKusec{apps: map[string]string{"kusec-payments-api-main": "app-1"}, audit: []kusecModel.AuditEntry{
		configItemUpdate(now.Add(-50*time.Minute), kusecModel.KubeKindConfigMap, "ACQUIRER_URL", "http://old.svc:8080", "http://acquirer-gateway.prod.svc:8080"),
		configItemUpdate(now.Add(-49*time.Minute), kusecModel.KubeKindConfigMap, "DB_PASSWORD", "old-secret", "new-secret"),
		configItemUpdate(now.Add(-48*time.Minute), kusecModel.KubeKindSecret, "ACQUIRER_KEY", "k1", "k2"),
		// чужой объект приложения и служебные записи в историю сервиса не попадают
		{CreatedAt: now.Add(-47 * time.Minute), EntityType: kusecModel.EntityItem, KubeKind: kusecModel.KubeKindSecret, KubeName: "kusec-other-main", Key: "X", Action: "update"},
		{CreatedAt: now.Add(-46 * time.Minute), EntityType: "api_key", Action: "create"},
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

	// секреты не протекают в события конфигурации
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
	// по кластеру — только сервисы с событиями, по свежести событий
	assert.Equal(t, lo.Uniq(lo.FilterMap(res.Events, func(e eventModel_Event, _ int) (string, bool) { return e.Service, e.Service != "" })), res.Services)
	assert.ElementsMatch(t, []string{"payments-api", "delivery"}, res.Services)
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
	kusec := &fakeKusec{
		apps:  map[string]string{"kusec-payments-api-main": "app-1"},
		audit: []kusecModel.AuditEntry{configItemUpdate(now, kusecModel.KubeKindConfigMap, "PG_DSN", "postgres://u:p@h/db", "postgres://u:p2@h/db")},
		drift: &kusecModel.Drift{InCluster: true},
	}

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
	assert.Equal(t, []string{"description"}, res.ConfigChanges[0].Fields)
	assert.NotNil(t, res.UnsyncedConfig, "kusec подключён — расхождений нет, но список есть")

	// сервис без задеплоенного коммита: unreleased отсутствует, kusec не подключён — в errors
	res, err = newUsecase(now, gh, nil, nil).Changes(context.Background(), "delivery", time.Hour)
	require.NoError(t, err)
	assert.Nil(t, res.Unreleased)
	assert.Equal(t, constant.SourceKusec, res.Errors[0].Source)
}

func TestChanges_ReloaderRollouts(t *testing.T) {
	now := time.Now().UTC()
	u := newUsecase(now, &fakeGithub{cmp: &githubModel.Comparison{}}, &fakeKusec{}, nil)
	reloaded := func(kind, name, hash string) map[string]string {
		return map[string]string{"reloader.stakater.com/last-reloaded-from": `{"type":"` + kind + `","name":"` + name + `","hash":"` + hash + `"}`}
	}
	u.k8s = &fakeK8s{replicaSets: []k8sModel.ReplicaSet{
		{Namespace: "prod", Name: "payments-api-1", OwnerKind: "Deployment", OwnerName: "payments-api", Revision: 1,
			CreatedAt: now.Add(-48 * time.Hour), TemplateAnnotations: reloaded("CONFIGMAP", "kusec-payments-api-main", "aaaaaaaaaaaaaaaa")},
		{Namespace: "prod", Name: "payments-api-2", OwnerKind: "Deployment", OwnerName: "payments-api", Revision: 2,
			CreatedAt: now.Add(-30 * time.Minute), TemplateAnnotations: reloaded("SECRET", "kusec-payments-api-main", "bbbbbbbbbbbbbbbb")},
		// чужой ReplicaSet под тем же селектором не учитывается
		{Namespace: "prod", Name: "other-2", OwnerKind: "Deployment", OwnerName: "other", Revision: 2,
			CreatedAt: now.Add(-10 * time.Minute), TemplateAnnotations: reloaded("CONFIGMAP", "x", "cccccccccccccccc")},
	}}

	res, err := u.Changes(context.Background(), "payments-api", time.Hour)
	require.NoError(t, err)
	require.Len(t, res.ConfigChanges, 1)
	change := res.ConfigChanges[0]
	assert.Equal(t, model.ConfigChangeSourceReloader, change.Source)
	assert.Equal(t, "prod/payments-api", change.Workload)
	assert.Equal(t, "secret", change.Kind)
	assert.Equal(t, "kusec-payments-api-main", change.Object)
	assert.Equal(t, "***", change.NewValue, "отпечаток secret не отдаётся")

	timeline, err := u.Timeline(context.Background(), &model.TimelineReq{Services: []string{"payments-api"}, Window: time.Hour})
	require.NoError(t, err)
	_, found := lo.Find(timeline.Events, func(e eventModel_Event) bool {
		return e.Type == constant.EventTypeConfigChange && e.Details["name"] == "kusec-payments-api-main"
	})
	assert.True(t, found, "выкатка reloader'а — событие config_change в таймлайне")
}

func TestChanges_KusecSyncLinkedToRollout(t *testing.T) {
	now := time.Now().UTC()
	kusec := &fakeKusec{
		apps: map[string]string{"kusec-payments-api-main": "app-1"},
		audit: []kusecModel.AuditEntry{
			configItemUpdate(now.Add(-40*time.Minute), kusecModel.KubeKindSecret, "PG_PASSWORD", "", ""),
		},
		runs: []kusecModel.SyncRun{
			{Id: "run-1", StartedAt: now.Add(-32 * time.Minute), Status: "ok", ActorName: "Dauren", Objects: []kusecModel.SyncObject{
				{KubeKind: "Secret", KubeName: "kusec-payments-api-main", Op: "updated", ChangedKeys: []string{"PG_PASSWORD"}},
				{KubeKind: "ConfigMap", KubeName: "kusec-payments-api-main", Op: "unchanged"},
			}},
			// sync без выкатки подов: отдельное изменение
			{Id: "run-2", StartedAt: now.Add(-5 * time.Minute), Status: "partial", ActorName: "Dauren", Objects: []kusecModel.SyncObject{
				{KubeKind: "ConfigMap", KubeName: "kusec-payments-api-main", Op: "updated", ChangedKeys: []string{"LOG_FORMAT"}},
			}},
		},
		drift: &kusecModel.Drift{InCluster: true, Objects: []kusecModel.DriftObject{
			{KubeKind: "ConfigMap", KubeName: "kusec-payments-api-main", ExistsInCluster: true, NotSyncedSince: new(now.Add(-2 * time.Minute)), MissingInCluster: []string{"NEW_KEY"}},
			{KubeKind: "Secret", KubeName: "kusec-payments-api-main", ExistsInCluster: true},
			{KubeKind: "Secret", KubeName: "kusec-other-main", ExistsInCluster: false},
		}},
	}
	u := newUsecase(now, &fakeGithub{cmp: &githubModel.Comparison{}}, kusec, nil)
	reloaded := func(hash string) map[string]string {
		return map[string]string{"reloader.stakater.com/last-reloaded-from": `{"type":"SECRET","name":"kusec-payments-api-main","hash":"` + hash + `"}`}
	}
	u.k8s = &fakeK8s{replicaSets: []k8sModel.ReplicaSet{
		{OwnerKind: "Deployment", OwnerName: "payments-api", Revision: 1, CreatedAt: now.Add(-48 * time.Hour), TemplateAnnotations: reloaded("aaaa")},
		{OwnerKind: "Deployment", OwnerName: "payments-api", Revision: 2, CreatedAt: now.Add(-31 * time.Minute), TemplateAnnotations: reloaded("bbbb")},
	}}

	res, err := u.Changes(context.Background(), "payments-api", time.Hour)
	require.NoError(t, err)
	assert.Empty(t, res.Errors)

	bySource := lo.GroupBy(res.ConfigChanges, func(c model.ConfigChange) string { return c.Source })
	require.Len(t, bySource[model.ConfigChangeSourceKusec], 1, "правка ключа")
	edit := bySource[model.ConfigChangeSourceKusec][0]
	assert.Equal(t, "PG_PASSWORD", edit.Key)
	assert.Equal(t, "***", edit.OldValue)

	require.Len(t, bySource[model.ConfigChangeSourceReloader], 1)
	rollout := bySource[model.ConfigChangeSourceReloader][0]
	assert.Equal(t, "run-1", rollout.SyncRunId, "выкатка склеена с sync, отдельного kusec_sync нет")
	assert.Equal(t, "Dauren", rollout.Author)
	assert.Equal(t, []string{"PG_PASSWORD"}, rollout.ChangedKeys)

	require.Len(t, bySource[model.ConfigChangeSourceKusecSync], 1)
	sync := bySource[model.ConfigChangeSourceKusecSync][0]
	assert.Equal(t, "run-2", sync.SyncRunId)
	assert.Equal(t, "partial", sync.Status)
	assert.Equal(t, []string{"LOG_FORMAT"}, sync.ChangedKeys)

	for i := 1; i < len(res.ConfigChanges); i++ {
		assert.False(t, res.ConfigChanges[i].TS.After(res.ConfigChanges[i-1].TS), "по убыванию времени")
	}

	require.Len(t, res.UnsyncedConfig, 1, "только объекты сервиса с расхождением")
	assert.Equal(t, []string{"NEW_KEY"}, res.UnsyncedConfig[0].MissingInCluster)

	timeline, err := u.Timeline(context.Background(), &model.TimelineReq{Services: []string{"payments-api"}, Window: time.Hour})
	require.NoError(t, err)
	configEvents := lo.Filter(timeline.Events, func(e eventModel_Event, _ int) bool { return e.Type == constant.EventTypeConfigChange })
	require.Len(t, configEvents, 3, "правка, sync с выкаткой одним событием, sync без выкатки")
	linked, _ := lo.Find(configEvents, func(e eventModel_Event) bool { return e.Details["sync_run_id"] == "run-1" })
	assert.Contains(t, linked.Summary, "PG_PASSWORD")
	assert.Contains(t, linked.Summary, "Dauren")
}

func TestTimeline_LongFiringAndMonitoringAlerts(t *testing.T) {
	now := time.Now().UTC()
	window := time.Hour
	points := func(from time.Time) []prometheusModel.Point {
		result := make([]prometheusModel.Point, 0)
		for ts := from; ts.Before(now); ts = ts.Add(time.Minute) {
			result = append(result, prometheusModel.Point{TS: ts, Value: 1})
		}
		return result
	}
	prom := &fakePrometheus{series: []prometheusModel.Series{
		// горит с прошлой недели: точки с самого начала окна
		{Labels: map[string]string{"alertname": "KubeProxyDown", "severity": "warning", "service": "payments-api", "instance": "10.0.0.1:10249", "prometheus": "prometheus/x"}, Points: points(now.Add(-window))},
		{Labels: map[string]string{"alertname": "Watchdog", "severity": "none"}, Points: points(now.Add(-window))},
		// сработал внутри окна
		{Labels: map[string]string{"alertname": "HighErrorRate", "severity": "critical", "service": "payments-api"}, Points: points(now.Add(-10 * time.Minute))},
	}}
	u := newUsecase(now, &fakeGithub{}, nil, prom)

	alerts := func(res *model.TimelineResult) []eventModel_Event {
		return lo.Filter(res.Events, func(e eventModel_Event, _ int) bool { return e.Type == constant.EventTypeAlertFiring })
	}

	cluster, err := u.Timeline(context.Background(), &model.TimelineReq{Scope: model.ScopeCluster, Window: window})
	require.NoError(t, err)
	clusterAlerts := alerts(cluster)
	require.Len(t, clusterAlerts, 1, "давно горящий и служебный алерты — не изменения")
	assert.Contains(t, clusterAlerts[0].Summary, "HighErrorRate")

	service, err := u.Timeline(context.Background(), &model.TimelineReq{Services: []string{"payments-api"}, Window: window})
	require.NoError(t, err)
	serviceAlerts := alerts(service)
	require.Len(t, serviceAlerts, 2)
	longFiring, _ := lo.Find(serviceAlerts, func(e eventModel_Event) bool { return strings.Contains(e.Summary, "KubeProxyDown") })
	assert.Contains(t, longFiring.Summary, "горит с начала окна")
	labels := longFiring.Details["labels"].(map[string]string)
	assert.NotContains(t, labels, "instance", "служебные лейблы не раздувают ответ")
	assert.Equal(t, "warning", labels["severity"])
}

func TestTimeline_RepeatedAlertsCollapse(t *testing.T) {
	now := time.Now().UTC()
	point := func(from time.Time, n int) []prometheusModel.Point {
		return lo.Map(lo.Range(n), func(i int, _ int) prometheusModel.Point {
			return prometheusModel.Point{TS: from.Add(time.Duration(i) * time.Minute), Value: 1}
		})
	}
	// KubeJobFailed по трём Job'ам CronJob'а payments-api-report: лейблы pod/container/service/job
	// принадлежат экспортеру kube-state-metrics, объект — job_name
	exporter := map[string]string{"alertname": "KubeJobFailed", "severity": "warning", "namespace": "prod", "job": "kube-state-metrics",
		"container": "kube-state-metrics", "pod": "prometheus-kube-state-metrics-1", "service": "prometheus-kube-state-metrics", "alertstate": "firing", "condition": "true"}
	job := func(name string, from time.Time) prometheusModel.Series {
		return prometheusModel.Series{Labels: lo.Assign(exporter, map[string]string{"job_name": name}), Points: point(from, 3)}
	}
	prom := &fakePrometheus{series: []prometheusModel.Series{
		job("payments-api-report-1", now.Add(-30*time.Minute)),
		job("payments-api-report-2", now.Add(-20*time.Minute)),
		job("payments-api-report-3", now.Add(-10*time.Minute)),
		{Labels: map[string]string{"alertname": "KubeJobFailed", "severity": "warning", "namespace": "other", "job": "kube-state-metrics", "job_name": "unknown-1"}, Points: point(now.Add(-10*time.Minute), 3)},
	}}
	u := newUsecase(now, &fakeGithub{}, nil, prom)

	res, err := u.Timeline(context.Background(), &model.TimelineReq{Scope: model.ScopeCluster, Window: time.Hour})
	require.NoError(t, err)
	alerts := lo.Filter(res.Events, func(e eventModel_Event, _ int) bool { return e.Type == constant.EventTypeAlertFiring })
	require.Len(t, alerts, 1, "повторы одного алерта у сервиса — одно событие; алерт без сервиса каталога не показывается")
	alert := alerts[0]
	assert.Equal(t, "payments-api", alert.Service, "не сервис экспортера")
	assert.Equal(t, now.Add(-30*time.Minute), alert.TS, "время первого срабатывания")
	assert.Contains(t, alert.Summary, "срабатывал 3 раз")
	assert.Equal(t, 3, alert.Details["count"])
	labels := alert.Details["labels"].(map[string]string)
	assert.Equal(t, "payments-api-report-1, payments-api-report-2, payments-api-report-3", labels["job_name"])
	for _, key := range []string{"pod", "container", "service", "alertstate", "condition"} {
		assert.NotContains(t, labels, key)
	}
}

func TestTimeline_PodsListedPerNamespaceInClusterScope(t *testing.T) {
	now := time.Now().UTC()
	u := newUsecase(now, &fakeGithub{}, nil, &fakePrometheus{})
	k8s := u.k8s.(*fakeK8s)
	k8s.pods = []k8sModel.Pod{
		{Name: "payments-api-1", Labels: map[string]string{"app": "payments-api"}, Containers: []k8sModel.PodContainer{{Name: "app", LastTerminationReason: "OOMKilled", LastTerminatedAt: now.Add(-5 * time.Minute), Restarts: 1}}},
		{Name: "delivery-1", Labels: map[string]string{"app": "delivery"}, Containers: []k8sModel.PodContainer{{Name: "app", LastTerminationReason: "Error", LastTerminatedAt: now.Add(-4 * time.Minute), Restarts: 2}}},
		{Name: "other-1", Labels: map[string]string{"app": "other"}, Containers: []k8sModel.PodContainer{{Name: "app", LastTerminationReason: "Error", LastTerminatedAt: now.Add(-3 * time.Minute), Restarts: 2}}},
	}
	wl := u.workload.(*fakeWorkload)
	for i := range perWorkloadMax {
		name := fmt.Sprintf("extra-%d", i)
		wl.items = append(wl.items, &workloadModel.Main{Cluster: "zeon", Namespace: "prod", Kind: "Deployment", Name: name, ServiceName: "delivery", Selector: "app=" + name})
	}

	res, err := u.Timeline(context.Background(), &model.TimelineReq{Scope: model.ScopeCluster, Window: time.Hour})
	require.NoError(t, err)
	assert.Equal(t, []string{"prod|"}, k8s.podCalls, "больше perWorkloadMax workload'ов в namespace — один список на namespace")

	// поды распределяются по workload'ам через селектор: чужой под (app=other) не попадает никому
	summaries := lo.Map(res.Events, func(e eventModel_Event, _ int) string { return e.Summary })
	assert.Contains(t, summaries, "payments-api: контейнер payments-api-1/app убит по OOM, всего рестартов 1")
	assert.Contains(t, summaries, "delivery: контейнер delivery-1/app перезапущен (Error), всего рестартов 2")
	assert.NotContains(t, strings.Join(summaries, "\n"), "other-1")
}

// TestChanges_LastCommitOutsideWindow — «о чём последний коммит?»: за окно коммитов нет —
// в ответе последний коммит ветки (случай seller из переписки с ботом).
func TestChanges_LastCommitOutsideWindow(t *testing.T) {
	now := time.Now().UTC()
	old := githubModel.Commit{SHA: "c0", Author: "a", Message: "old fix", Date: now.Add(-30 * 24 * time.Hour)}

	res, err := newUsecase(now, &fakeGithub{older: []githubModel.Commit{old}, cmp: &githubModel.Comparison{}}, &fakeKusec{}, nil).
		Changes(context.Background(), "payments-api", 7*24*time.Hour)
	require.NoError(t, err)
	assert.Empty(t, res.Commits)
	require.NotNil(t, res.LastCommit)
	assert.Equal(t, "c0", res.LastCommit.SHA)
	assert.Equal(t, "old fix", res.LastCommit.Message)

	// коммиты в окне есть — last_commit не нужен
	res, err = newUsecase(now, &fakeGithub{commits: []githubModel.Commit{{SHA: "c1", Date: now}}, older: []githubModel.Commit{old}, cmp: &githubModel.Comparison{}}, &fakeKusec{}, nil).
		Changes(context.Background(), "payments-api", time.Hour)
	require.NoError(t, err)
	assert.Nil(t, res.LastCommit)
}
