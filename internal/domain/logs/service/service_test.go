package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse/internal/domain/logs/model"
)

func TestNormalize(t *testing.T) {
	s := New()
	cases := map[string]string{
		"dial tcp 10.42.1.7:8080: connect: connection refused":    "dial tcp <IP>: connect: connection refused",
		"order 3f2b1c0e-1a2b-4c3d-9e8f-123456789abc not found":    "order <UUID> not found",
		"2026-09-03T08:46:50.211Z request took 1523ms status=500": "<TS> request took <NUM> status=<NUM>",
		"retry   attempt 3 of 5 after 2.5s":                       "retry attempt <NUM> of <NUM> after <NUM>",
		"trace 9f86d081884c7d659a2feaa0c55ad015 failed":           "trace <HEX> failed",
	}
	for in, want := range cases {
		assert.Equal(t, want, s.Normalize(in), in)
	}
}

func TestDetectLevel(t *testing.T) {
	s := New()
	assert.Equal(t, model.LevelError, s.DetectLevel(`{"level":"error","msg":"x"}`))
	assert.Equal(t, model.LevelError, s.DetectLevel(`{"level":"ERROR","msg":"x"}`))
	assert.Equal(t, model.LevelWarn, s.DetectLevel(`time=... level=warning msg=x`))
	assert.Equal(t, model.LevelError, s.DetectLevel(`2026-09-03 08:00:00 [ERROR] boom`))
	assert.Equal(t, model.LevelInfo, s.DetectLevel(`INFO started`))
	assert.Equal(t, "", s.DetectLevel(`plain text without level`))
}

func TestMessageOf(t *testing.T) {
	s := New()
	assert.Equal(t, "acquirer timeout: dial tcp 1.2.3.4:443: i/o timeout",
		s.MessageOf(`{"time":"2026-09-03T08:00:00Z","level":"error","msg":"acquirer timeout","error":"dial tcp 1.2.3.4:443: i/o timeout"}`))
	assert.Equal(t, "plain", s.MessageOf("  plain "))
	assert.Equal(t, `{"foo":"bar"}`, s.MessageOf(`{"foo":"bar"}`), "json без msg — как есть")
}

// критерий приёмки фазы 3: 10 000 строк → ≤ 20 паттернов
func TestAggregate_Compresses(t *testing.T) {
	s := New()
	now := time.Now()
	lines := make([]model.Line, 0, 10000)
	for i := 0; i < 10000; i++ {
		var text string
		switch i % 4 {
		case 0:
			text = fmt.Sprintf("dial tcp 10.42.%d.%d:8080: connect: connection refused", i%255, i%13)
		case 1:
			text = fmt.Sprintf(`{"level":"error","msg":"acquirer timeout","error":"request %s timed out after %dms"}`, fmt.Sprintf("%08x-1111-2222-3333-%012x", i, i), 1000+i)
		case 2:
			text = fmt.Sprintf("2026-09-03T08:%02d:%02dZ INFO handled request id=%d in %dms", i%60, i%60, i, i%900)
		default:
			text = fmt.Sprintf("WARN slow query took %d.%dms", i, i%10)
		}
		lines = append(lines, model.Line{TS: now.Add(time.Duration(-i) * time.Second), Level: s.DetectLevel(text), Text: text})
	}

	patterns := s.Aggregate(lines, 20)
	require.LessOrEqual(t, len(patterns), 20)
	require.Len(t, patterns, 4)
	assert.Equal(t, 2500, patterns[0].Count)
	assert.NotEmpty(t, patterns[0].Example)
	assert.True(t, patterns[0].FirstSeen.Before(patterns[0].LastSeen))

	byLevel := map[string]int{}
	for _, p := range patterns {
		byLevel[p.Level]++
	}
	assert.Equal(t, 1, byLevel[model.LevelError])
	assert.Equal(t, 1, byLevel[""], "строка без уровня")
	assert.Equal(t, 1, byLevel[model.LevelWarn])
	assert.Equal(t, 1, byLevel[model.LevelInfo])
}
