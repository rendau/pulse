package imageref

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Ref
	}{
		{"ghcr.io/mechta-market/promo-sync:latest", Ref{Host: "ghcr.io", Path: "mechta-market/promo-sync", Tag: "latest"}},
		{"ghcr.io/mechta-market/kafka_producer", Ref{Host: "ghcr.io", Path: "mechta-market/kafka_producer"}},
		{"ghcr.io/org/app@sha256:abc", Ref{Host: "ghcr.io", Path: "org/app", Digest: "sha256:abc"}},
		{"ghcr.io/org/app:v1@sha256:abc", Ref{Host: "ghcr.io", Path: "org/app", Tag: "v1", Digest: "sha256:abc"}},
		{"postgres:17-bullseye", Ref{Host: "docker.io", Path: "library/postgres", Tag: "17-bullseye"}},
		{"redis", Ref{Host: "docker.io", Path: "library/redis"}},
		{"bitnami/redis:7", Ref{Host: "docker.io", Path: "bitnami/redis", Tag: "7"}},
		{"localhost:5000/app:dev", Ref{Host: "localhost:5000", Path: "app", Tag: "dev"}},
		{"registry.company.kz:443/team/app:a3f9c21", Ref{Host: "registry.company.kz:443", Path: "team/app", Tag: "a3f9c21"}},
	}

	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := Parse(c.in)
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}

	_, err := Parse("")
	assert.Error(t, err)
}

func TestRefHelpers(t *testing.T) {
	ref, err := Parse("ghcr.io/mechta-market/promo-sync:latest")
	require.NoError(t, err)

	assert.Equal(t, "promo-sync", ref.Name())
	assert.Equal(t, "mechta-market", ref.Org())
	assert.Equal(t, "mechta-market/promo-sync", ref.Repo())
	assert.Equal(t, "promo-sync", ref.RepoName())

	// образ внутри репозитория (несколько пакетов из одной репы)
	nested, err := Parse("ghcr.io/rendau/loom/server:latest")
	require.NoError(t, err)
	assert.Equal(t, "server", nested.Name())
	assert.Equal(t, "rendau/loom", nested.Repo())
	assert.Equal(t, "loom", nested.RepoName())

	single, err := Parse("localhost:5000/app:dev")
	require.NoError(t, err)
	assert.Equal(t, "app", single.Repo())
	assert.Equal(t, "app", single.RepoName())
	assert.Equal(t, "latest", ref.Reference())
	assert.Equal(t, "ghcr.io/mechta-market/promo-sync:latest", ref.String())

	withDigest := ref.WithDigest("sha256:abc")
	assert.Equal(t, "sha256:abc", withDigest.Reference())
	assert.Equal(t, "ghcr.io/mechta-market/promo-sync@sha256:abc", withDigest.String())
	assert.Equal(t, "latest", ref.Tag, "WithDigest не мутирует исходник")
}

func TestLooksLikeCommit(t *testing.T) {
	assert.True(t, LooksLikeCommit("a3f9c21"))
	assert.True(t, LooksLikeCommit("a3f9c21b7e1d02a3f9c21b7e1d02a3f9c21b7e1d"))
	assert.False(t, LooksLikeCommit("latest"))
	assert.False(t, LooksLikeCommit("v1.4.5"))
	assert.False(t, LooksLikeCommit("abc"))
	assert.False(t, LooksLikeCommit("A3F9C21"))
}

func TestDigestFromImageID(t *testing.T) {
	assert.Equal(t, "sha256:abc", DigestFromImageID("ghcr.io/org/app@sha256:abc"))
	assert.Equal(t, "sha256:abc", DigestFromImageID("docker-pullable://ghcr.io/org/app@sha256:abc"))
	assert.Equal(t, "sha256:abc", DigestFromImageID("sha256:abc"))
	assert.Equal(t, "", DigestFromImageID("docker://deadbeef"))
	assert.Equal(t, "", DigestFromImageID(""))
}
