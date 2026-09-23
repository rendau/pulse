package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-github/v82/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackageRepoUrl(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		linked := func(fullName string) {
			_ = json.NewEncoder(w).Encode(github.Package{Repository: &github.Repository{FullName: new(fullName)}})
		}
		switch r.URL.EscapedPath() {
		// пакет организации с именем, не совпадающим с репой
		case "/orgs/mechta-market/packages/container/dpm":
			linked("mechta-market/dp-mechta")
		// пакет пользователя с вложенным именем: org → 404, затем user
		case "/users/rendau/packages/container/loom%2Fserver":
			linked("rendau/loom")
		// пакет без привязки к репозиторию
		case "/orgs/mechta-market/packages/container/manual":
			_ = json.NewEncoder(w).Encode(github.Package{})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}
	}))
	defer srv.Close()

	client := github.NewClient(nil)
	var err error
	client.BaseURL, err = client.BaseURL.Parse(srv.URL + "/")
	require.NoError(t, err)
	s := &Service{client: client}
	ctx := context.Background()

	cases := map[string]string{
		"mechta-market/dpm":    "https://github.com/mechta-market/dp-mechta",
		"rendau/loom/server":   "https://github.com/rendau/loom",
		"mechta-market/manual": "",
		"stakater/reloader":    "",
	}
	for imagePath, want := range cases {
		got, err := s.PackageRepoUrl(ctx, imagePath)
		require.NoError(t, err, imagePath)
		assert.Equal(t, want, got, imagePath)
	}

	before := calls
	got, err := s.PackageRepoUrl(ctx, "mechta-market/dpm")
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/mechta-market/dp-mechta", got)
	assert.Equal(t, before, calls, "повторный запрос — из кэша")
}
