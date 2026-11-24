package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/pulse/internal/errs"
	pulseagentModel "github.com/rendau/pulse/internal/service/pulseagent/model"
)

func TestSendFinding(t *testing.T) {
	var got map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/findings", r.URL.Path)
		assert.Equal(t, "Bearer k1", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		_, _ = w.Write([]byte(`{"id":42,"deduplicated":true}`))
	}))
	defer ts.Close()

	rep, err := New(ts.URL+"/", "k1").SendFinding(context.Background(), &pulseagentModel.Finding{
		Key: "security:198.51.100.7", Kind: "security", Severity: "critical", Title: "t", Text: "x", Source: "claude-routine",
	})
	require.NoError(t, err)
	assert.Equal(t, &pulseagentModel.FindingResult{Id: 42, Deduplicated: true}, rep)
	assert.Equal(t, map[string]any{"key": "security:198.51.100.7", "kind": "security", "severity": "critical",
		"title": "t", "text": "x", "source": "claude-routine"}, got)
}

func TestSendFindingErrors(t *testing.T) {
	status := http.StatusTooManyRequests
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"x"}`))
	}))
	defer ts.Close()
	s := New(ts.URL, "k1")

	_, err := s.SendFinding(context.Background(), &pulseagentModel.Finding{})
	require.ErrorIs(t, err, pulseagentModel.ErrRateLimited)

	status = http.StatusUnauthorized
	_, err = s.SendFinding(context.Background(), &pulseagentModel.Finding{})
	require.ErrorIs(t, err, errs.ServiceNA)
	assert.Contains(t, err.Error(), "status 401")
	assert.NotContains(t, err.Error(), "k1")
}
