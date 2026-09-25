package logs

import (
	"context"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	logsModel "github.com/mechta-market/pulse/internal/domain/logs/model"
	logsService "github.com/mechta-market/pulse/internal/domain/logs/service"
	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	"github.com/mechta-market/pulse/internal/errs"
	lokiModel "github.com/mechta-market/pulse/internal/service/loki/model"
	lokiService "github.com/mechta-market/pulse/internal/service/loki/service"
	"github.com/mechta-market/pulse/internal/usecase/logs/model"
)

func newClusterUsecase(loki LokiI) *Usecase {
	wl := &fakeWorkload{items: []*workloadModel.Main{
		{Namespace: "prod", Kind: "Deployment", Name: "orders", ServiceName: "orders"},
		{Namespace: "prod", Kind: "Deployment", Name: "orders-worker", ServiceName: "orders"},
		{Namespace: "prod", Kind: "Deployment", Name: "sms", ServiceName: "sms"},
		{Namespace: "prod", Kind: "Deployment", Name: "sms-im", ServiceName: "sms-im"},
	}}
	return New(Config{MaxLines: 5000, MaxPatterns: 20, RawLimit: 100, MaxWindow: 24 * time.Hour},
		&fakeSvc{service: &svcModel.Main{}}, wl, &fakeK8s{}, loki, logsService.New())
}

func streamLabels(namespace, pod string) map[string]string {
	return map[string]string{"kubernetes_namespace_name": namespace, "kubernetes_pod_name": pod}
}

func TestQuery_SearchAllServices(t *testing.T) {
	now := time.Now()
	loki := &fakeLoki{streams: []lokiModel.Stream{
		{Labels: streamLabels("prod", "orders-worker-7d9f-q2"), Entries: []lokiModel.Entry{
			{TS: now.Add(-time.Minute), Line: `{"level":"info","msg":"order ORD-12345 paid","phone":"+77011234567"}`},
			{TS: now.Add(-3 * time.Minute), Line: `{"level":"info","msg":"order ORD-12345 created"}`},
		}},
		{Labels: streamLabels("prod", "sms-im-5c6d-x1"), Entries: []lokiModel.Entry{
			{TS: now.Add(-2 * time.Minute), Line: `level=info msg="sms sent" order=ORD-12345`},
		}},
		{Labels: streamLabels("infra", "gateway-1"), Entries: []lokiModel.Entry{
			{TS: now.Add(-4 * time.Minute), Line: `GET /orders/ORD-12345 200`},
		}},
	}}
	u := newClusterUsecase(loki)

	res, err := u.Query(context.Background(), &model.QueryReq{Pattern: "ORD-12345", Mode: model.ModeRaw})
	require.NoError(t, err)

	assert.Equal(t, `{kubernetes_namespace_name=~".+"} |= "ORD-12345"`, loki.query)
	assert.Empty(t, res.Service)
	assert.Equal(t, 4, res.TotalLines)
	require.Len(t, res.Services, 3)
	assert.Equal(t, "orders", res.Services[0].Service)
	assert.Equal(t, 2, res.Services[0].Count)
	assert.Equal(t, now.Add(-3*time.Minute), res.Services[0].FirstSeen)

	require.Len(t, res.Lines, 4)
	assert.Equal(t, "orders", res.Lines[0].Service)
	assert.Equal(t, "orders-worker", res.Lines[0].Workload, "самый длинный префикс workload'а")
	assert.NotContains(t, res.Lines[0].Text, "7011234567", "PII маскируется и при поиске")
	assert.Equal(t, "sms-im", res.Lines[1].Service, "sms-im-…, а не sms")
	assert.Empty(t, res.Lines[3].Service, "под не из каталога")
	assert.Equal(t, "infra", res.Lines[3].Namespace)

	res, err = u.Query(context.Background(), &model.QueryReq{Pattern: `ORD-\d+`})
	require.NoError(t, err)
	assert.Contains(t, loki.query, `|~ "ORD-\\d+"`)
	require.Len(t, res.Patterns, 4)
	services := lo.FlatMap(res.Patterns, func(p logsModel.Pattern, _ int) []string { return p.Services })
	assert.ElementsMatch(t, []string{"orders", "orders", "sms-im"}, services)
	assert.True(t, lo.EveryBy(res.Patterns, func(p logsModel.Pattern) bool { return len(p.Workloads) == 0 }))
}

func TestQuery_SearchValidation(t *testing.T) {
	u := newClusterUsecase(&fakeLoki{})

	_, err := u.Query(context.Background(), &model.QueryReq{})
	require.ErrorIs(t, err, errs.InvalidRequest)
	assert.ErrorContains(t, err, "service is required")

	_, err = u.Query(context.Background(), &model.QueryReq{Pattern: "12"})
	require.ErrorIs(t, err, errs.InvalidRequest)

	_, err = u.Query(context.Background(), &model.QueryReq{Pattern: "12345", Workload: "orders"})
	require.ErrorIs(t, err, errs.InvalidRequest)

	_, err = newClusterUsecase(nil).Query(context.Background(), &model.QueryReq{Pattern: "12345"})
	require.ErrorIs(t, err, errs.ServiceNA)
	assert.ErrorContains(t, err, "requires Loki")
}

func TestClusterErrors(t *testing.T) {
	now := time.Now()
	loki := &fakeLoki{
		vector: []lokiModel.Sample{
			{Labels: streamLabels("prod", "orders-7d9f-q2"), Value: 30},
			{Labels: streamLabels("prod", "orders-worker-5c6d-x1"), Value: 12},
			{Labels: streamLabels("prod", "sms-8f7e-a1"), Value: 5},
			{Labels: streamLabels("infra", "gateway-1"), Value: 3},
			{Labels: streamLabels("prod", "sms-8f7e-a2"), Value: 0},
		},
		streams: []lokiModel.Stream{
			{Labels: streamLabels("prod", "orders-7d9f-q2"), Entries: []lokiModel.Entry{
				{TS: now.Add(-time.Minute), Line: `{"level":"error","msg":"get order 101","error":"context deadline exceeded"}`},
				{TS: now.Add(-2 * time.Minute), Line: `{"level":"error","msg":"get order 102","error":"context deadline exceeded"}`},
			}},
		},
	}
	u := newClusterUsecase(loki)

	res, err := u.ClusterErrors(context.Background(), 48*time.Hour, 2)
	require.NoError(t, err)

	assert.Equal(t, 24*time.Hour, res.Window, "окно ограничено потолком логов")
	assert.Contains(t, loki.vectorQuery, "count_over_time(")
	assert.Contains(t, loki.vectorQuery, "[86400s]")
	assert.Contains(t, loki.vectorQuery, "sum by (namespace, kubernetes_namespace_name")

	assert.Equal(t, 50, res.Total)
	assert.Equal(t, 3, res.ServicesTotal)
	require.Len(t, res.Services, 2)
	assert.Equal(t, "orders", res.Services[0].Service)
	assert.Equal(t, 42, res.Services[0].Count, "workload'ы одного сервиса суммируются")
	assert.Equal(t, 2, res.Services[0].Top.Count)
	assert.Equal(t, "get order <NUM>: context deadline exceeded", res.Services[0].Top.Template)
	assert.Equal(t, "sms", res.Services[1].Service)
	assert.Zero(t, res.Services[1].Top.Count, "строк сервиса нет в выборке — без паттерна")

	_, err = newClusterUsecase(nil).ClusterErrors(context.Background(), time.Hour, 10)
	require.ErrorIs(t, err, errs.ServiceNA)
}

func TestErrorLineRe(t *testing.T) {
	re := regexp.MustCompile(errorLineRe)
	for line, want := range map[string]bool{
		`{"level":"error","msg":"boom"}`:                       true,
		`level=ERROR msg="boom"`:                               true,
		`{"log":"{\"level\":\"error\",\"msg\":\"boom\"}\n"}`:   true,
		`2026-09-25 ERROR payment failed`:                      true,
		`panic: runtime error: index out of range`:             true,
		`{"severity":"CRITICAL"}`:                              true,
		`{"level":"info","msg":"no error, errors=0"}`:          false,
		`level=info msg="retry after error"`:                   false,
		`{"log":"{\"level\":\"info\",\"msg\":\"error=nil\"}"}`: false,
	} {
		assert.Equal(t, want, re.MatchString(line), line)
	}
}

// Живой прогон запросов против настоящего Loki (синтаксис LogQL, sum by по отсутствующим
// лейблам): LOKI_LIVE_URL=http://localhost:3100 go test ./internal/usecase/logs/ -run TestLive -v
func TestLive_ClusterQueries(t *testing.T) {
	url := os.Getenv("LOKI_LIVE_URL")
	if url == "" {
		t.Skip("LOKI_LIVE_URL is not set")
	}
	u := newClusterUsecase(lokiService.New(url, lokiService.Auth{}))

	errors, err := u.ClusterErrors(context.Background(), time.Hour, 10)
	require.NoError(t, err)
	t.Logf("cluster errors: total=%d services=%d", errors.Total, errors.ServicesTotal)
	for _, s := range errors.Services {
		t.Logf("  %s/%s: %d, top=%q", s.Namespace, s.Service, s.Count, s.Top.Template)
	}

	search, err := u.Query(context.Background(), &model.QueryReq{Pattern: "ORD-12345", Mode: model.ModeRaw})
	require.NoError(t, err)
	for _, h := range search.Services {
		t.Logf("  hits %s/%s: %d", h.Namespace, h.Service, h.Count)
	}
	for _, l := range search.Lines {
		t.Logf("  line %s %s: %s", l.Service, l.Workload, l.Text)
	}

	_, err = u.Query(context.Background(), &model.QueryReq{Pattern: `ORD-\d+`, Level: "info"})
	require.NoError(t, err)
}
