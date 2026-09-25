package logs

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	logsService "github.com/mechta-market/pulse/internal/domain/logs/service"
	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
	lokiModel "github.com/mechta-market/pulse/internal/service/loki/model"
	"github.com/mechta-market/pulse/internal/usecase/logs/model"
)

type fakeSvc struct{ service *svcModel.Main }

func (f *fakeSvc) GetOrSuggest(context.Context, string) (*svcModel.Main, error) {
	return f.service, nil
}

type fakeWorkload struct{ items []*workloadModel.Main }

func (f *fakeWorkload) List(context.Context, *workloadModel.ListReq) ([]*workloadModel.Main, int64, error) {
	return f.items, int64(len(f.items)), nil
}

type fakeK8s struct {
	pods []k8sModel.Pod
	// logs — строки по «под/контейнер»; previous — с суффиксом «/previous»
	logs    map[string][]k8sModel.LogLine
	logsErr error
}

func (f *fakeK8s) ListPods(context.Context, string, string) ([]k8sModel.Pod, error) {
	return f.pods, nil
}

func (f *fakeK8s) PodLogs(_ context.Context, _, pod, container string, _ time.Time, _ int64, previous bool) ([]k8sModel.LogLine, error) {
	key := pod + "/" + container
	if previous {
		key += "/previous"
	}
	return f.logs[key], f.logsErr
}

type fakeLoki struct {
	query   string
	limit   int
	streams []lokiModel.Stream
	err     error

	vectorQuery string
	vector      []lokiModel.Sample
	vectorErr   error
}

func (f *fakeLoki) QueryVector(_ context.Context, query string, _ time.Time) ([]lokiModel.Sample, error) {
	f.vectorQuery = query
	return f.vector, f.vectorErr
}

func (f *fakeLoki) QueryRange(_ context.Context, query string, _, _ time.Time, limit int) ([]lokiModel.Stream, error) {
	f.query, f.limit = query, limit
	return f.streams, f.err
}

func newUsecase(loki LokiI, selector string) *Usecase {
	return newUsecaseWithPods(loki, selector, nil)
}

func newUsecaseWithPods(loki LokiI, selector string, pods []k8sModel.Pod) *Usecase {
	svc := &fakeSvc{service: &svcModel.Main{Name: "payments-api"}}
	svc.service.Metadata.Logs.Selector = selector
	wl := &fakeWorkload{items: []*workloadModel.Main{
		{Namespace: "prod", Kind: "Deployment", Name: "payments-api", Selector: "app=payments-api"},
		{Namespace: "prod", Kind: "CronJob", Name: "payments-api-reconcile"},
	}}
	return New(Config{MaxLines: 5000, MaxPatterns: 20, RawLimit: 100, MaxWindow: 24 * time.Hour,
		DefaultSelector: `{namespace="{namespace}", pod=~"{pod_regex}"}`}, svc, wl, &fakeK8s{pods: pods}, loki, logsService.New())
}

func sampleStreams(now time.Time) []lokiModel.Stream {
	entries := make([]lokiModel.Entry, 0, 60)
	for i := 0; i < 50; i++ {
		entries = append(entries, lokiModel.Entry{TS: now.Add(time.Duration(-i) * time.Second),
			Line: fmt.Sprintf(`{"level":"error","msg":"acquirer timeout","error":"dial tcp 10.0.0.%d:443: i/o timeout"}`, i)})
	}
	for i := 0; i < 10; i++ {
		entries = append(entries, lokiModel.Entry{TS: now.Add(time.Duration(-i)*time.Minute - 30*time.Second), Line: "INFO request handled in 12ms"})
	}
	return []lokiModel.Stream{{Labels: map[string]string{"app": "payments-api"}, Entries: entries}}
}

func TestQuery_PatternsWithDerivedSelector(t *testing.T) {
	now := time.Now()
	loki := &fakeLoki{streams: sampleStreams(now)}
	u := newUsecase(loki, "")

	res, err := u.Query(context.Background(), &model.QueryReq{Service: "payments-api", Level: "error"})
	require.NoError(t, err)

	assert.Equal(t, `{namespace="prod", pod=~"^(payments-api|payments-api-reconcile)-.*"}`, res.Selector)
	assert.Contains(t, loki.query, res.Selector)
	assert.Contains(t, loki.query, `|~ "(?i)error|fatal|panic|critical"`)
	assert.Equal(t, 5000, loki.limit)

	assert.Equal(t, model.ModePatterns, res.Mode)
	assert.Equal(t, 50, res.TotalLines, "info-строки отфильтрованы по уровню")
	assert.False(t, res.Truncated)
	require.Len(t, res.Patterns, 1)
	assert.Equal(t, 50, res.Patterns[0].Count)
	assert.Equal(t, "acquirer timeout: dial tcp <IP>: i/o timeout", res.Patterns[0].Template)
	assert.Equal(t, "error", res.Patterns[0].Level)
}

func TestQuery_RawAndSelectorFromYaml(t *testing.T) {
	now := time.Now()
	loki := &fakeLoki{streams: sampleStreams(now)}
	u := newUsecase(loki, `{app="payments-api"}`)

	res, err := u.Query(context.Background(), &model.QueryReq{Service: "payments-api", Mode: "raw", Limit: 5, Pattern: "timeout"})
	require.NoError(t, err)

	assert.Equal(t, `{app="payments-api"}`, res.Selector)
	assert.Contains(t, loki.query, `|= "timeout"`, "паттерн без метасимволов — подстрокой")
	assert.Equal(t, 5, loki.limit)
	assert.Len(t, res.Lines, 5)
	assert.True(t, res.Truncated)
	assert.True(t, res.Lines[0].TS.After(res.Lines[1].TS), "новые первыми")
}

func TestQuery_Validation(t *testing.T) {
	ctx := context.Background()
	u := newUsecase(&fakeLoki{}, "")

	_, err := u.Query(ctx, &model.QueryReq{Service: "payments-api", Level: "verbose"})
	assert.ErrorContains(t, err, "level")

	_, err = u.Query(ctx, &model.QueryReq{Service: "payments-api", Pattern: "("})
	assert.ErrorContains(t, err, "regexp")

	_, err = u.Query(ctx, &model.QueryReq{Service: "payments-api", Window: 48 * time.Hour})
	assert.ErrorContains(t, err, "exceeds maximum")

	_, err = u.Query(ctx, &model.QueryReq{Service: "payments-api", Mode: "stream"})
	assert.ErrorContains(t, err, "mode")

	// без Loki — запасной источник Kubernetes, а не ошибка
	res, err := newUsecase(nil, "").Query(ctx, &model.QueryReq{Service: "payments-api"})
	require.NoError(t, err)
	assert.Equal(t, model.SourceKubernetes, res.Source)

	// без привязки к сервису запрос невозможен: нет workloads и нет селектора
	empty := New(Config{DefaultSelector: "{x}"}, &fakeSvc{service: &svcModel.Main{Name: "ghost"}}, &fakeWorkload{}, &fakeK8s{}, &fakeLoki{}, logsService.New())
	_, err = empty.Query(ctx, &model.QueryReq{Service: "ghost"})
	assert.ErrorContains(t, err, "no workloads")

	// Loki не ответил, и Kubernetes тоже — в ошибке оба источника
	failing := newUsecaseWithPods(&fakeLoki{err: errors.New("503")}, "", []k8sModel.Pod{
		{Namespace: "prod", Name: "payments-api-7d9f-q2", Containers: []k8sModel.PodContainer{{Name: "app"}}},
	})
	failing.k8s.(*fakeK8s).logsErr = errors.New("forbidden")
	_, err = failing.Query(ctx, &model.QueryReq{Service: "payments-api"})
	assert.ErrorContains(t, err, "loki.QueryRange")
	assert.ErrorContains(t, err, "kubernetes fallback")
}

// TestQuery_KubernetesFallback — Loki не подключён: логи живых подов сервиса из Kubernetes API,
// фильтры уровня и регэкспа — на стороне pulse, прошлый запуск перезапускавшегося контейнера,
// PII маскируется, чужие поды не читаются.
func TestQuery_KubernetesFallback(t *testing.T) {
	now := time.Now()
	pods := []k8sModel.Pod{
		{Namespace: "prod", Name: "payments-api-7d9f-q2", StartedAt: now.Add(-time.Hour), Containers: []k8sModel.PodContainer{
			{Name: "app", Restarts: 2, LastTerminatedAt: now.Add(-10 * time.Minute)},
		}},
		{Namespace: "prod", Name: "other-service-1-x", Containers: []k8sModel.PodContainer{{Name: "app"}}},
	}
	u := newUsecaseWithPods(nil, "", pods)
	u.k8s.(*fakeK8s).logs = map[string][]k8sModel.LogLine{
		"payments-api-7d9f-q2/app": {
			{TS: now.Add(-time.Minute), Text: `{"level":"error","msg":"acquirer timeout","error":"call 77021330032 failed"}`},
			{TS: now.Add(-2 * time.Minute), Text: `INFO request handled in 12ms`},
		},
		"payments-api-7d9f-q2/app/previous": {
			{TS: now.Add(-11 * time.Minute), Text: `{"level":"error","msg":"panic: nil map"}`},
		},
		"other-service-1-x/app": {{TS: now, Text: "ERROR not ours"}},
	}

	res, err := u.Query(context.Background(), &model.QueryReq{Service: "payments-api", Level: "error", Mode: model.ModeRaw})
	require.NoError(t, err)
	assert.Equal(t, model.SourceKubernetes, res.Source)
	assert.Contains(t, res.Selector, "payments-api")
	require.Len(t, res.Lines, 2, "error-строки текущего и прошлого запуска; чужой под не читается")
	assert.Equal(t, "payments-api", res.Lines[0].Workload)
	assert.NotContains(t, res.Lines[0].Text, "77021330032")
	assert.Contains(t, res.Lines[1].Text, "panic: nil map")

	res, err = u.Query(context.Background(), &model.QueryReq{Service: "payments-api", Pattern: "handled", Mode: model.ModeRaw})
	require.NoError(t, err)
	require.Len(t, res.Lines, 1)
}

func TestTopErrors(t *testing.T) {
	now := time.Now()
	loki := &fakeLoki{streams: sampleStreams(now)}
	u := newUsecase(loki, "")

	patterns, err := u.TopErrors(context.Background(), &svcModel.Main{Name: "payments-api"},
		[]*workloadModel.Main{{Namespace: "prod", Name: "payments-api"}}, time.Hour, 3)
	require.NoError(t, err)
	require.Len(t, patterns, 1)
	assert.Equal(t, 50, patterns[0].Count)
	assert.Contains(t, loki.query, "error|fatal")
}

// TestQuery_WorkloadsAndPII — из какого workload'а строка (самый длинный префикс пода), фильтр
// по workload'у и маскирование PII (случай notifire-sms из переписки с ботом).
func TestQuery_WorkloadsAndPII(t *testing.T) {
	now := time.Now()
	line := `{"level":"error","msg":"sms_traffic: fail to send: context deadline exceeded, phone: 77021330032"}`
	loki := &fakeLoki{streams: []lokiModel.Stream{
		{Labels: map[string]string{"kubernetes_pod_name": "payments-api-reconcile-29001-x1"}, Entries: []lokiModel.Entry{{TS: now, Line: line}}},
		{Labels: map[string]string{"pod": "payments-api-7d9f-q2"}, Entries: []lokiModel.Entry{{TS: now.Add(-time.Second), Line: line}}},
	}}
	u := newUsecase(loki, "")

	res, err := u.Query(context.Background(), &model.QueryReq{Service: "payments-api", Level: "error"})
	require.NoError(t, err)
	require.Len(t, res.Patterns, 1)
	assert.Equal(t, []string{"payments-api", "payments-api-reconcile"}, res.Patterns[0].Workloads)
	assert.NotContains(t, res.Patterns[0].Example, "77021330032")
	assert.NotContains(t, res.Patterns[0].Template, "77021330032")

	res, err = u.Query(context.Background(), &model.QueryReq{Service: "payments-api", Mode: model.ModeRaw, Workload: "payments-api-reconcile"})
	require.NoError(t, err)
	assert.Equal(t, `{namespace="prod", pod=~"^(payments-api-reconcile)-.*"}`, res.Selector)
	require.Len(t, res.Lines, 1)
	assert.Equal(t, "payments-api-reconcile", res.Lines[0].Workload)
	assert.NotContains(t, res.Lines[0].Text, "77021330032")

	_, err = u.Query(context.Background(), &model.QueryReq{Service: "payments-api", Workload: "notifire-sms"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "payments-api, payments-api-reconcile", "в ошибке — список workload'ов сервиса")
}

// TestQuery_OrchestratorJobs — логи Job'ов, которые создаёт оркестратор (loom и т.п.): поды
// без workload'а сервиса, но с managed-by сервиса или его образом, — в логах сервиса по общему
// префиксу имён Job'ов (он покрывает и уже удалённые поды).
func TestQuery_OrchestratorJobs(t *testing.T) {
	job := func(pod, jobName, managedBy, image string) k8sModel.Pod {
		return k8sModel.Pod{Namespace: "prod", Name: pod,
			Labels:     map[string]string{"batch.kubernetes.io/job-name": jobName, "app.kubernetes.io/managed-by": managedBy},
			Containers: []k8sModel.PodContainer{{Image: image}}}
	}
	pods := []k8sModel.Pod{
		{Namespace: "prod", Name: "payments-api-7d9f-q2"}, // workload сервиса
		job("pa-sync-1-abc-x1", "pa-sync-1-abc", "payments-api", "ghcr.io/org/dags/dags:latest"),
		job("pa-sync-2-def-x2", "pa-sync-2-def", "payments-api", "ghcr.io/org/dags/dags:latest"),
		job("pa-report-1-aaa-x3", "pa-report-1-aaa", "", "ghcr.io/org/payments-api:v1"), // по образу
		job("other-1-bbb-x4", "other-1-bbb", "someone-else", "ghcr.io/org/other:v1"),    // чужой
	}
	now := time.Now()
	loki := &fakeLoki{streams: []lokiModel.Stream{
		{Labels: map[string]string{"pod": "pa-sync-9-old-gone"}, Entries: []lokiModel.Entry{{TS: now, Line: "ERROR task failed: no rows"}}},
	}}
	u := newUsecaseWithPods(loki, "", pods)

	res, err := u.Query(context.Background(), &model.QueryReq{Service: "payments-api", Level: "error"})
	require.NoError(t, err)
	assert.Equal(t, `{namespace="prod", pod=~"^((payments-api|payments-api-reconcile)-.*|pa-.*)"}`, res.Selector)
	require.Len(t, res.Patterns, 1)
	assert.Equal(t, []string{"pa-*"}, res.Patterns[0].Workloads, "удалённый под — по префиксу Job'ов")

	// только Job'ы
	res, err = u.Query(context.Background(), &model.QueryReq{Service: "payments-api", Workload: "pa-*"})
	require.NoError(t, err)
	assert.Equal(t, `{namespace="prod", pod=~"^(pa-.*)"}`, res.Selector)
}
