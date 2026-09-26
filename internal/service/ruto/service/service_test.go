package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rutoModel "github.com/rendau/pulse/internal/service/ruto/model"
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

func TestParseGatewayError(t *testing.T) {
	s := New("http://ruto")
	re := regexp.MustCompile(s.GatewayErrorsFilter())

	proxy := `{"time":"2026-09-26T09:13:32.58+05:00","level":"ERROR","msg":"proxy error POST /send/service/send","reason":"backend closed connection","error":"EOF","app_name":"sms_acc_service"}`
	assert.True(t, re.MatchString(proxy))
	e, ok := s.ParseGatewayError(proxy)
	require.True(t, ok)
	assert.Equal(t, rutoModel.GatewayError{Kind: rutoModel.GatewayErrorProxy, AppName: "sms_acc_service", Reason: "backend closed connection", Error: "EOF"}, *e)

	script := `{"level":"ERROR","msg":"request transform: compile failed","error":"SyntaxError: Unexpected token","app_id":"a1","endpoint_id":"e1"}`
	assert.True(t, re.MatchString(script))
	e, ok = s.ParseGatewayError(script)
	require.True(t, ok)
	assert.Equal(t, rutoModel.GatewayError{Kind: rutoModel.GatewayErrorScript, AppId: "a1", EndpointId: "e1", Reason: "request transform: compile failed", Error: "SyntaxError: Unexpected token"}, *e)

	e, ok = s.ParseGatewayError(`{"msg":"response transform: run failed","error":"TypeError","app_id":"a1","endpoint_id":"e2"}`)
	require.True(t, ok)
	assert.Equal(t, "response transform: run failed", e.Reason)

	for _, line := range []string{
		`{"msg":"proxy error GET /x","reason":"client canceled request","app_name":"a"}`,
		`{"msg":"proxy error GET /x","reason":"backend connection refused"}`,
		`proxy error GET /x: EOF`,
		`{"msg":"request","status":"500"}`,
	} {
		_, ok = s.ParseGatewayError(line)
		assert.False(t, ok, line)
	}
}
