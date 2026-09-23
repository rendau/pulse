package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-github/v82/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func run(sha, branch string, started, finished time.Time) *github.WorkflowRun {
	return &github.WorkflowRun{
		HeadSHA: new(sha), HeadBranch: new(branch),
		RunStartedAt: &github.Timestamp{Time: started}, UpdatedAt: &github.Timestamp{Time: finished},
	}
}

func TestPickBuildRun(t *testing.T) {
	published := time.Date(2026, 9, 17, 4, 30, 0, 0, time.UTC)
	at := func(m int) time.Time { return published.Add(time.Duration(m) * time.Minute) }

	runs := []*github.WorkflowRun{
		run("later", "master", at(5), at(9)),
		run("develop", "develop", at(-4), at(1)),
		run("master", "master", at(-6), at(1)),
		run("earlier", "master", at(-30), at(-20)),
	}
	assert.Equal(t, "master", pickBuildRun(runs, published, "master"), "параллельная сборка ветки develop уступает ветке по умолчанию")

	// публикация чуть позже завершения запуска (updated_at) — в пределах допуска
	assert.Equal(t, "earlier", pickBuildRun([]*github.WorkflowRun{run("earlier", "master", at(-30), at(-1))}, published, "master"))

	assert.Empty(t, pickBuildRun([]*github.WorkflowRun{run("x", "master", at(-30), at(-20))}, published, "master"),
		"ни один запуск не покрывает публикацию — коммит не угадывается")
}

// TestResolveImageCommit_UserOwnedNestedPackage — пакет пользователя с вложенным именем
// (ghcr.io/rendau/loom/server): org-запрос отвечает 404, user-запрос должен получить
// экранированное имя loom%2Fserver, иначе GitHub не находит пакет.
func TestResolveImageCommit_UserOwnedNestedPackage(t *testing.T) {
	published := time.Date(2026, 9, 17, 10, 8, 1, 0, time.UTC)
	digest := "sha256:4fd803f5"
	var paths []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath())
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.EscapedPath() {
		case "/orgs/rendau/packages/container/loom%2Fserver/versions":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		case "/users/rendau/packages/container/loom%2Fserver/versions":
			_ = json.NewEncoder(w).Encode([]*github.PackageVersion{{Name: new(digest), CreatedAt: &github.Timestamp{Time: published}}})
		case "/repos/rendau/loom/actions/runs":
			_ = json.NewEncoder(w).Encode(github.WorkflowRuns{WorkflowRuns: []*github.WorkflowRun{
				run("d266f741d5eb684456fff8564b83632c070fed19", "main", published.Add(-5*time.Minute), published.Add(11*time.Second)),
			}})
		case "/repos/rendau/loom":
			_ = json.NewEncoder(w).Encode(github.Repository{DefaultBranch: new("main")})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"unexpected ` + r.URL.EscapedPath() + `"}`))
		}
	}))
	defer srv.Close()

	client := github.NewClient(nil)
	var err error
	client.BaseURL, err = client.BaseURL.Parse(srv.URL + "/")
	require.NoError(t, err)
	s := &Service{client: client}

	sha, err := s.ResolveImageCommit(context.Background(), "https://github.com/rendau/loom", "rendau/loom/server", digest)
	require.NoError(t, err)
	assert.Equal(t, "d266f741d5eb684456fff8564b83632c070fed19", sha)
	assert.Contains(t, paths, "/users/rendau/packages/container/loom%2Fserver/versions")
}
