package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	piiServiceP "github.com/mechta-market/pulse/internal/service/pii/service"
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
	  ],
	  "entities": [
	    {"name": "доставка", "status": "degraded", "created_1h": 120, "finished_1h": -1, "statuses": [
	      {"name": "assigned", "count": 42, "stuck": 50, "oldest_s": 5400},
	      {"name": "broken", "count": -3}
	    ]}
	  ]
	}`)}}
	pii := piiServiceP.New(piiServiceP.Config{})
	s := New(Config{Path: "/.well-known/pulse/status", CacheTtl: time.Minute}, pods, pii)
	target := svcproxyModel.PodTarget{Namespace: "prod", Pod: "ocenter-1", IP: "10.0.0.5", Port: 3003}

	status, err := s.Get(context.Background(), target)
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.Equal(t, "degraded", status.Status)
	assert.False(t, status.CheckedAt.IsZero())
	require.Len(t, status.Dependencies, 2, "записи не по стандарту пропущены")
	assert.Equal(t, int64(4200), *status.Dependencies[1].LatencyMs)
	assert.NotContains(t, status.Dependencies[1].Message, "s3cr3t", "учётные данные вырезаны")
	require.Len(t, status.Gauges, 2, "значение-текст не принимается")
	assert.InDelta(t, 1840, *status.Gauges[0].Value, 0)
	assert.NotNil(t, status.Gauges[1].Time)
	require.Len(t, status.Entities, 1)
	e := status.Entities[0]
	assert.Equal(t, int64(120), *e.Created1h)
	assert.Nil(t, e.Finished1h, "отрицательное — не принимается")
	require.Len(t, e.Statuses, 1, "отрицательный счётчик — не принимается")
	assert.Equal(t, int64(42), e.Statuses[0].Stuck, "застрявших не больше, чем всего")
	assert.Equal(t, 90*time.Minute, e.Statuses[0].Oldest)

	_, err = s.Get(context.Background(), target)
	require.NoError(t, err)
	assert.Equal(t, 1, pods.calls, "второй раз — из кэша")

	// ручки состояния нет
	pods.resp = &svcproxyModel.Response{StatusCode: 404}
	status, err = New(Config{Path: "/s"}, pods, pii).Get(context.Background(), target)
	require.NoError(t, err)
	assert.Nil(t, status)

	// не по стандарту
	pods.resp = &svcproxyModel.Response{StatusCode: 200, Body: []byte(`{"status":"fine"}`)}
	_, err = New(Config{Path: "/s"}, pods, pii).Get(context.Background(), target)
	assert.Error(t, err)
}
