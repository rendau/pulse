package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthHeaders(t *testing.T) {
	var got http.Header
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ctx := context.Background()

	t.Run("bearer + org id", func(t *testing.T) {
		require.NoError(t, New(ts.URL, Auth{Token: "t0k", OrgId: "tenant-1"}).Ping(ctx))
		assert.Equal(t, "Bearer t0k", got.Get("Authorization"))
		assert.Equal(t, "tenant-1", got.Get("X-Scope-OrgID"))
	})

	t.Run("basic auth from url userinfo", func(t *testing.T) {
		u := "http://user:pass@" + ts.Listener.Addr().String()
		require.NoError(t, New(u, Auth{}).Ping(ctx))
		user, pass, ok := (&http.Request{Header: got}).BasicAuth()
		require.True(t, ok)
		assert.Equal(t, "user", user)
		assert.Equal(t, "pass", pass)
	})
}

func TestQueryDecode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/query":
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"a"},"value":[1700000000.5,"0.31"]}]}}`))
		case "/api/v1/query_range":
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1700000000,"1"],[1700000060,"2"]]}]}}`))
		default:
			_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"parse error"}`))
		}
	}))
	defer ts.Close()

	s := New(ts.URL, Auth{})
	ctx := context.Background()

	samples, err := s.Query(ctx, "x", time.Time{})
	require.NoError(t, err)
	require.Len(t, samples, 1)
	assert.Equal(t, 0.31, samples[0].Value)
	assert.Equal(t, "a", samples[0].Labels["job"])
	assert.Equal(t, int64(1700000000), samples[0].TS.Unix())

	series, err := s.QueryRange(ctx, "x", time.Unix(1700000000, 0), time.Unix(1700000060, 0), time.Minute)
	require.NoError(t, err)
	require.Len(t, series, 1)
	assert.Len(t, series[0].Points, 2)
}
