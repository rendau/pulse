package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kusecModel "github.com/rendau/pulse/internal/service/kusec/model"
)

var auditRecords = []string{
	`{"id":"42","created_at":"2026-09-17T09:12:30Z","actor_usr_id":"1","actor_name":"Dauren","source":"ui","request_id":"",
	 "entity_type":"item","entity_id":"i1","app_id":"app-1","namespace":"default","app_slug":"caravan",
	 "kube_kind":"Secret","kube_name":"kusec-caravan-main","key":"PG_PASSWORD","action":"update",
	 "changes":[{"field":"value","old_hash":"a1b2","new_hash":"c3d4","old_size":"32","new_size":"48","truncated":false}]}`,
	`{"id":"41","created_at":"2026-09-17T09:10:00Z","actor_name":"Dauren","source":"ui","request_id":"",
	 "entity_type":"config_item","entity_id":"c1","app_id":"app-1","namespace":"default","app_slug":"caravan",
	 "kube_kind":"ConfigMap","kube_name":"kusec-caravan-main","key":"LOG_FORMAT","action":"create",
	 "changes":[{"field":"value","new":"json","old_hash":"","new_hash":"","truncated":false}],"batch_id":"b1"}`,
	`{"id":"40","created_at":"2026-09-17T09:00:00Z","actor_name":"x","source":"api","entity_type":"secret","action":"delete","changes":[]}`,
}

func newServer(t *testing.T, resolves *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ksk_test" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":"not_authorized","message":"no session"}`))
			return
		}
		q := r.URL.Query()
		switch r.URL.Path {
		case "/api/app":
			_, _ = w.Write([]byte(`{"pagination_info":{},"results":[]}`))
		case "/api/app/resolve":
			resolves.Add(1)
			if q.Get("kube_name") != "kusec-caravan-main" {
				_, _ = w.Write([]byte(`{"found":false,"kube_kind":"","object_id":"","object_slug":""}`))
				return
			}
			_, _ = w.Write([]byte(`{"found":true,"app":{"id":"app-1","namespace":"default","slug_name":"caravan","name":"Caravan"},"kube_kind":"Secret","object_id":"s1","object_slug":"main"}`))
		case "/api/audit":
			assert.Equal(t, "app-1", q.Get("app_id"))
			assert.Equal(t, "2026-09-17T04:00:00Z", q.Get("created_at_gte"))
			page, _ := strconv.Atoi(q.Get("list_params.page"))
			size, _ := strconv.Atoi(q.Get("list_params.page_size"))
			from, to := min(page*size, len(auditRecords)), min(page*size+size, len(auditRecords))
			_, _ = w.Write([]byte(`{"pagination_info":{},"results":[` + strings.Join(auditRecords[from:to], ",") + `]}`))
		case "/api/sync-run":
			_, _ = w.Write([]byte(`{"results":[{"id":"run-1","started_at":"2026-09-17T09:13:00Z","status":"ok","error":"","actor_name":"Dauren","source":"ui","app_id":"app-1","duration_ms":"120","objects":[]}]}`))
		case "/api/sync-run/run-1":
			_, _ = w.Write([]byte(`{"id":"run-1","started_at":"2026-09-17T09:13:00Z","finished_at":"2026-09-17T09:13:01Z","status":"ok","error":"","actor_name":"Dauren","source":"ui","app_id":"app-1","duration_ms":"120",
				"objects":[{"namespace":"default","kube_kind":"Secret","kube_name":"kusec-caravan-main","op":"updated","error":"","content_hash":"ffff","changed_keys":["PG_PASSWORD"]}]}`))
		case "/api/app/app-1/drift":
			_, _ = w.Write([]byte(`{"in_cluster":true,"objects":[
				{"kube_kind":"Secret","kube_name":"kusec-caravan-main","namespace":"default","object_id":"s1","exists_in_cluster":true,"managed":true,
				 "missing_in_cluster":["NEW_KEY"],"extra_in_cluster":[],"value_differs":[],"not_synced_since":"2026-09-17T09:20:00Z"},
				{"kube_kind":"ConfigMap","kube_name":"kusec-caravan-main","namespace":"default","object_id":"c1","exists_in_cluster":true,"managed":true,
				 "missing_in_cluster":[],"extra_in_cluster":[],"value_differs":[]}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, "service path not found")
		}
	}))
}

func TestClient(t *testing.T) {
	var resolves atomic.Int32
	srv := newServer(t, &resolves)
	defer srv.Close()
	ctx := context.Background()

	// адрес с /api на конце тоже принимается
	s := New(srv.URL+"/api/", Auth{Token: "ksk_test"})

	require.NoError(t, s.Ping(ctx))

	resolved, err := s.Resolve(ctx, "default", "kusec-caravan-main")
	require.NoError(t, err)
	assert.Equal(t, &kusecModel.Resolved{Found: true, AppId: "app-1", AppSlug: "caravan", Namespace: "default", KubeKind: "Secret", ObjectId: "s1"}, resolved)
	_, _ = s.Resolve(ctx, "default", "kusec-caravan-main")
	assert.Equal(t, int32(1), resolves.Load(), "resolve кэшируется")

	foreign, err := s.Resolve(ctx, "default", "istio-ca-root-cert")
	require.NoError(t, err)
	assert.False(t, foreign.Found)

	since := time.Date(2026, 9, 17, 4, 0, 0, 0, time.UTC)
	audit, err := s.ListAudit(ctx, &kusecModel.AuditReq{AppId: "app-1", Since: since, Limit: 2})
	require.NoError(t, err)
	require.Len(t, audit, 2, "limit обрезает, вторая страница не нужна")
	assert.Equal(t, int64(42), audit[0].Id)
	secret := audit[0].Changes[0]
	assert.Nil(t, secret.Old)
	assert.Equal(t, "c3d4", secret.NewHash)
	assert.Equal(t, int64(48), *secret.NewSize)
	assert.Equal(t, "json", *audit[1].Changes[0].New)
	assert.Nil(t, audit[1].Changes[0].NewSize, "отсутствующее optional-поле")
	assert.Equal(t, "b1", audit[1].BatchId)

	all, err := s.ListAudit(ctx, &kusecModel.AuditReq{AppId: "app-1", Since: since, Limit: 150})
	require.NoError(t, err)
	assert.Len(t, all, 3, "неполная первая страница — последняя")

	paged, err := s.ListAudit(ctx, &kusecModel.AuditReq{AppId: "app-1", Since: since, Limit: 3})
	require.NoError(t, err)
	assert.Len(t, paged, 3)

	runs, err := s.ListSyncRuns(ctx, &kusecModel.SyncRunReq{AppId: "app-1", Limit: 10})
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Len(t, runs[0].Objects, 1, "объекты подтягиваются через Get")
	assert.Equal(t, []string{"PG_PASSWORD"}, runs[0].Objects[0].ChangedKeys)
	assert.Equal(t, int64(120), runs[0].DurationMs)
	require.NotNil(t, runs[0].FinishedAt)

	drift, err := s.GetDrift(ctx, "app-1")
	require.NoError(t, err)
	assert.True(t, drift.InCluster)
	assert.True(t, drift.Objects[0].HasDrift())
	assert.False(t, drift.Objects[1].HasDrift())

	err = New(srv.URL, Auth{Token: "wrong"}).Ping(ctx)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "not_authorized"), err.Error())
}
