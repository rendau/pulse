package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// conf.example.yml копируется в образ как conf.yml — он обязан парситься.
func TestLoadRules_Example(t *testing.T) {
	rules, err := LoadRules("../../conf.example.yml")
	require.NoError(t, err)

	assert.Equal(t, time.Hour, rules.Indexer.StaleAfter)
	assert.NotEmpty(t, rules.ImageMapping)
	assert.Equal(t, 5*time.Second, rules.Snapshot.Deadline)
	assert.NotEmpty(t, rules.Snapshot.DefaultMetrics, "без секции берётся встроенный набор")
	assert.Equal(t, 5000, rules.Logs.MaxLines)
	assert.Equal(t, 24*time.Hour, rules.Logs.MaxWindow)
	assert.Equal(t, 20, rules.Metrics.MaxSeries)
}

func TestLoadRules_Missing(t *testing.T) {
	rules, err := LoadRules("/nonexistent/conf.yml")
	require.NoError(t, err)
	assert.Equal(t, 3, rules.Logs.TopErrors)
	assert.Equal(t, `{kubernetes_namespace_name="{namespace}", kubernetes_pod_name=~"{pod_regex}"}`, rules.Logs.DefaultSelector,
		"лейблы fluent-bit в Loki")
	assert.Equal(t, "ruto-gateway", rules.Ruto.GatewayService)
	require.Len(t, rules.Snapshot.PublicMetrics, 3)
	for _, m := range rules.Snapshot.PublicMetrics {
		assert.Contains(t, m.PromQL, `app=~"{ruto_apps}"`)
		assert.Contains(t, m.PromQL, "mechta_ruto_gw_http_")
	}
}

// Запросы pulse к манифесту и ручке состояния — не трафик сервиса: встроенные golden signals
// не считают их ни в одном селекторе метрик запросов (404 на поиске манифеста давал error_rate).
func TestDefaultMetrics_WithoutPulseRequests(t *testing.T) {
	rules, err := LoadRules("/nonexistent/conf.yml")
	require.NoError(t, err)

	promql := map[string]string{}
	for _, m := range rules.Snapshot.DefaultMetrics {
		promql[m.Id] = m.PromQL
	}
	for _, id := range []string{"rps", "error_rate", "rejected_rate", "latency_p95"} {
		selectors := strings.Count(promql[id], "__name__=~")
		require.Positive(t, selectors, id)
		assert.Equal(t, selectors, strings.Count(promql[id], `path_name!~"/\\.well-known/pulse(/.*)?"`), id)
	}
	assert.NotContains(t, promql["cpu_cores"], "path_name", "у метрик контейнера пути нет")
}

// Встроенные метрики собираются из настроек файла: путь манифеста, метрики gateway.
func TestLoadRules_DerivedMetrics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conf.yml")
	require.NoError(t, os.WriteFile(path, []byte("manifest:\n  path: /_pulse\nruto:\n  requests_metric: gw_requests_total\n"), 0o600))

	rules, err := LoadRules(path)
	require.NoError(t, err)
	for _, m := range rules.Snapshot.DefaultMetrics {
		assert.NotContains(t, m.PromQL, "well-known", m.Id)
		if m.Id == "error_rate" {
			assert.Contains(t, m.PromQL, `path_name!~"/_pulse(/.*)?"`)
		}
	}
	assert.Contains(t, rules.Snapshot.PublicMetrics[0].PromQL, "gw_requests_total{")

	// свои метрики в файле — как есть
	require.NoError(t, os.WriteFile(path, []byte("snapshot:\n  default_metrics:\n    - id: rps\n      promql: up\n"), 0o600))
	rules, err = LoadRules(path)
	require.NoError(t, err)
	require.Len(t, rules.Snapshot.DefaultMetrics, 1)
	assert.Equal(t, "up", rules.Snapshot.DefaultMetrics[0].PromQL)
}
