package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	svcproxyModel "github.com/mechta-market/pulse/internal/service/svcproxy/model"
)

type fakePods struct {
	resp  *svcproxyModel.Response
	calls int
}

func (f *fakePods) GetPod(_ context.Context, _ svcproxyModel.PodTarget, path string, _, _ map[string]string, _ int64) (*svcproxyModel.Response, error) {
	f.calls++
	return f.resp, nil
}

func TestGet(t *testing.T) {
	pods := &fakePods{resp: &svcproxyModel.Response{StatusCode: 200, Body: []byte(`{
	  "status": "degraded",
	  "checked_at": "2026-09-25T19:07:40+05:00",
	  "dependencies": [
	    {"id": "pg", "status": "ok", "latency_ms": 3},
	    {"id": "onec", "status": "degraded", "latency_ms": 4200, "message": "dial postgres://app:s3cr3t@onec-db:5432: timeout, клиент +7 701 123 45 67"},
	    {"id": "Bad Id", "status": "ok"},
	    {"id": "x", "status": "sick"}
	  ],
	  "gauges": [
	    {"id": "outbox_backlog", "title": "Неотправленные события", "value": 1840, "unit": "count", "status": "degraded"},
	    {"id": "last_sync", "title": "Последняя выгрузка", "value": "2026-09-25T18:52:00+05:00", "unit": "time", "status": "ok"},
	    {"id": "note", "title": "Заметка", "value": "любой текст"}
	  ]
	}`)}}
	s := New(Config{Path: "/.well-known/pulse/status", CacheTtl: time.Minute}, pods)
	target := svcproxyModel.PodTarget{Namespace: "prod", Pod: "ocenter-1", IP: "10.0.0.5", Port: 3003}

	status, err := s.Get(context.Background(), target)
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.Equal(t, "degraded", status.Status)
	assert.False(t, status.CheckedAt.IsZero())
	require.Len(t, status.Dependencies, 2, "записи не по стандарту пропущены")
	assert.Equal(t, int64(4200), *status.Dependencies[1].LatencyMs)
	assert.NotContains(t, status.Dependencies[1].Message, "s3cr3t", "учётные данные вырезаны")
	assert.NotContains(t, status.Dependencies[1].Message, "701 123", "телефон замаскирован")
	require.Len(t, status.Gauges, 2, "значение-текст не принимается")
	assert.InDelta(t, 1840, *status.Gauges[0].Value, 0)
	assert.NotNil(t, status.Gauges[1].Time)

	_, err = s.Get(context.Background(), target)
	require.NoError(t, err)
	assert.Equal(t, 1, pods.calls, "второй раз — из кэша")

	// ручки состояния нет
	pods.resp = &svcproxyModel.Response{StatusCode: 404}
	status, err = New(Config{Path: "/s"}, pods).Get(context.Background(), target)
	require.NoError(t, err)
	assert.Nil(t, status)

	// не по стандарту
	pods.resp = &svcproxyModel.Response{StatusCode: 200, Body: []byte(`{"status":"fine"}`)}
	_, err = New(Config{Path: "/s"}, pods).Get(context.Background(), target)
	assert.Error(t, err)
}
