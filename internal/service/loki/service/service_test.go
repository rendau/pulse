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

func TestQueryRangeDecode(t *testing.T) {
	var gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		assert.Equal(t, "backward", r.URL.Query().Get("direction"))
		assert.Equal(t, "tenant", r.Header.Get("X-Scope-OrgID"))
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[
			{"stream":{"app":"x","level":"error"},"values":[["1700000001000000000","dial tcp 10.0.0.1:8080: connection refused"],["1700000000000000000","ok"]]}]}}`))
	}))
	defer ts.Close()

	streams, err := New(ts.URL, Auth{OrgId: "tenant"}).QueryRange(context.Background(), `{app="x"}`, time.Unix(1699999000, 0), time.Unix(1700000100, 0), 100)
	require.NoError(t, err)
	assert.Equal(t, `{app="x"}`, gotQuery)
	require.Len(t, streams, 1)
	assert.Equal(t, "x", streams[0].Labels["app"])
	require.Len(t, streams[0].Entries, 2)
	assert.Equal(t, int64(1700000001), streams[0].Entries[0].TS.Unix())
	assert.Contains(t, streams[0].Entries[0].Line, "connection refused")
}
