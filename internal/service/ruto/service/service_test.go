package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rutoModel "github.com/mechta-market/pulse/internal/service/ruto/model"
)

const snapshotBody = `{"data":{
	"base_url":"https://api.mdev.kz",
	"auth":{"enabled":true,"methods":[{"basic":{"users":[{"username":"u","password":"root-secret"}]}}]},
	"variables":{"TOKEN":"root-var-secret"},
	"apps":[{
		"id":"a1","name":"ocenter","active":true,"path_prefix":"/ocenter",
		"backend":{"url":"http://ocenter.default.svc","grpc_url":"","headers":{"X-Api-Key":"backend-secret"}},
		"auth":{"methods":[{"api_key":{"keys":[{"name":"k","key":"app-secret"}]}}]},
		"endpoints":[
			{"id":"e1","active":true,"type":"","http":{"method":"get","path":"order/{id}"}},
			{"id":"e2","active":true,"type":"grpc","grpc":{"path":"/ocenter_v1.Ord/Get"}}
		]
	}]
}}`

func TestGetSnapshot(t *testing.T) {
	var snapshots atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case versionPath:
			_, _ = w.Write([]byte(`{"version":"v1"}`))
		case snapshotPath:
			snapshots.Add(1)
			_, _ = w.Write([]byte(snapshotBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	s := New(srv.URL)
	snap, err := s.GetSnapshot(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "v1", snap.Version)
	assert.Equal(t, "https://api.mdev.kz", snap.BaseUrl)
	require.Len(t, snap.Apps, 1)
	app := snap.Apps[0]
	assert.Equal(t, "http://ocenter.default.svc", app.BackendUrl)
	require.Len(t, app.Endpoints, 2)
	assert.Equal(t, "GET /ocenter/order/{id}", app.Route(app.Endpoints[0]), "пустой type — http, метод в верхнем регистре")
	assert.Equal(t, "GRPC (ocenter)/ocenter_v1.Ord/Get", app.Route(app.Endpoints[1]))
	assert.NotContains(t, snapshotString(snap), "secret", "секреты снапшота не разбираются")

	// версия не менялась — снапшот из кэша (проверка версии тоже кэшируется на versionTTL)
	s.checkedAt = s.checkedAt.Add(-2 * versionTTL)
	_, err = s.GetSnapshot(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(1), snapshots.Load())
}

func snapshotString(v *rutoModel.Snapshot) string {
	out := v.BaseUrl + v.Version
	for _, a := range v.Apps {
		out += a.Id + a.Name + a.PathPrefix + a.BackendUrl + a.GrpcUrl
		for _, e := range a.Endpoints {
			out += e.Id + e.Type + e.Method + e.Path + e.GrpcPath
		}
	}
	return out
}
