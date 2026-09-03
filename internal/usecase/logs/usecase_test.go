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

type fakeLoki struct {
	query   string
	limit   int
	streams []lokiModel.Stream
	err     error
}

func (f *fakeLoki) QueryRange(_ context.Context, query string, _, _ time.Time, limit int) ([]lokiModel.Stream, error) {
	f.query, f.limit = query, limit
	return f.streams, f.err
}

func newUsecase(loki LokiI, selector string) *Usecase {
	svc := &fakeSvc{service: &svcModel.Main{Name: "payments-api"}}
	svc.service.Metadata.Logs.Selector = selector
	wl := &fakeWorkload{items: []*workloadModel.Main{
		{Namespace: "prod", Kind: "Deployment", Name: "payments-api", Selector: "app=payments-api"},
		{Namespace: "prod", Kind: "CronJob", Name: "payments-api-reconcile"},
	}}
	return New(Config{MaxLines: 5000, MaxPatterns: 20, RawLimit: 100, MaxWindow: 24 * time.Hour,
		DefaultSelector: `{namespace="{namespace}", pod=~"{pod_regex}"}`}, svc, wl, loki, logsService.New())
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
	assert.Contains(t, loki.query, `|~ "timeout"`)
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

	_, err = newUsecase(nil, "").Query(ctx, &model.QueryReq{Service: "payments-api"})
	assert.ErrorContains(t, err, "LOKI_URL")

	// без привязки к сервису запрос невозможен: нет workloads и нет селектора
	empty := New(Config{DefaultSelector: "{x}"}, &fakeSvc{service: &svcModel.Main{Name: "ghost"}}, &fakeWorkload{}, &fakeLoki{}, logsService.New())
	_, err = empty.Query(ctx, &model.QueryReq{Service: "ghost"})
	assert.ErrorContains(t, err, "no workloads")

	_, err = newUsecase(&fakeLoki{err: errors.New("503")}, "").Query(ctx, &model.QueryReq{Service: "payments-api"})
	assert.ErrorContains(t, err, "loki.QueryRange")
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
