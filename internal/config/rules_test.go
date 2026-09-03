package config

import (
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
	assert.Contains(t, rules.Logs.DefaultSelector, "{namespace}")
}
