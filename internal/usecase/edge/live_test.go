package edge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	edgeService "github.com/rendau/pulse/internal/domain/edge/service"
	lokiService "github.com/rendau/pulse/internal/service/loki/service"
	"github.com/rendau/pulse/internal/usecase/edge/model"
)

// Живой прогон запросов журнала против Loki в docker (CLAUDE.md, «Тестовый стенд»):
// EDGE_LOKI_LIVE_URL=http://localhost:3110 go test ./internal/usecase/edge/ -run TestLive -v
// Пишет в Loki строки traefik со своим лейблом прогона и сверяет общие запросы длинного окна
// (адреса с кодами, признаки взлома вместе с ручками) с раздельными.
func TestLive_LongWindowMergedQueries(t *testing.T) {
	url := os.Getenv("EDGE_LOKI_LIVE_URL")
	if url == "" {
		t.Skip("EDGE_LOKI_LIVE_URL is not set")
	}
	run := strconv.FormatInt(time.Now().UnixNano(), 36)
	now := time.Now()
	type req struct {
		ip, path string
		status   int
		n        int
	}
	reqs := []req{
		{"198.51.100.1", "/api/v1/items", 200, 40},
		{"198.51.100.1", "/api/v1/items/42", 404, 5},
		{"198.51.100.2", "/.env", 404, 3},
		{"198.51.100.2", "/static/..%2f..%2fetc/passwd", 400, 2},
		{"198.51.100.3", "/auth/sync_perms", 200, 4},
		{"198.51.100.3", "/admin/sync_perms", 403, 1},
		{"198.51.100.4", "/v1/account/me", 200, 7},
		{"198.51.100.5", "/swagger/index.html", 200, 2},
		{"198.51.100.5", "/ab/v1/payment/123", 500, 1},
		{"198.51.100.6", "/docsearch", 200, 3}, // не docs: признака нет
	}
	var values [][]string
	i := 0
	for _, r := range reqs {
		for range r.n {
			i++
			line, _ := json.Marshal(map[string]any{
				edgeService.FieldClientHost: r.ip, edgeService.FieldHost: "api.example.com", edgeService.FieldPath: r.path,
				edgeService.FieldStatus: r.status, edgeService.FieldRouter: "websecure-ext@kubernetes",
			})
			ts := now.Add(-time.Duration(i) * 30 * time.Second)
			values = append(values, []string{strconv.FormatInt(ts.UnixNano(), 10), string(line)})
		}
	}
	body, _ := json.Marshal(map[string]any{"streams": []map[string]any{{
		"stream": map[string]string{"job": "edge-live", "run": run}, "values": values,
	}}})
	resp, err := http.Post(url+"/loki/api/v1/push", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	_ = resp.Body.Close()
	time.Sleep(2 * time.Second)

	u := New(Config{Selector: fmt.Sprintf(`{job="edge-live", run=%q}`, run), InternalRouterPrefix: "websecure-int-",
		Sensitive: []string{"sync_perms", "/ab/v1/payment", "/account/"}}, lokiService.New(url, lokiService.Auth{}))
	r := u.newRun(context.Background(), time.Now())
	base := u.base("", false)
	rng := rangeOf(3 * time.Hour)

	clients := clientSet{}
	sig, sens, hits, merged := r.pathSignals(base, rng, clients)
	require.True(t, merged, "общий запрос прошёл: %v", r.errors)
	sepSig := r.signatures(base, rng, 3*time.Hour, clientSet{})
	sepSens, sepHits := r.sensitive(base, rng, nil, clientSet{})
	assert.Equal(t, sepSig, sig)
	assert.Equal(t, sepSens, sens)
	assert.Equal(t, sepHits, hits)
	assert.ElementsMatch(t, []string{"198.51.100.2", "198.51.100.3", "198.51.100.5"}, lo.Keys(sig), ".3 — /admin/…, не /docsearch")
	assert.Empty(t, r.errors)

	// сводка за длинное окно: коды — из шага 1, совпадают с отдельным запросом
	res, err := u.Traffic(context.Background(), &model.TrafficReq{Window: 3 * time.Hour})
	require.NoError(t, err)
	assert.Empty(t, res.Errors)
	assert.Equal(t, r.statuses(base, rng), res.Statuses)
	assert.Equal(t, float64(i), res.Requests)
	assert.Equal(t, 4, res.Queries, "адреса с кодами, признаки с ручками, подробности, пути подозрительных")
	t.Logf("queries: %d, statuses: %v", res.Queries, res.Statuses)
	for _, c := range res.Clients {
		t.Logf("  %s req=%v sig=%v sens=%v paths=%d", c.Addr, c.Requests, c.SignatureHits, c.SensitiveHits, c.DistinctPaths)
	}
}
