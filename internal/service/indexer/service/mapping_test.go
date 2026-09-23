package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	indexerModel "github.com/mechta-market/pulse/internal/service/indexer/model"
	"github.com/mechta-market/pulse/internal/util/imageref"
)

func TestImageMapper(t *testing.T) {
	mapper := newImageMapper([]indexerModel.ImageMapping{
		{Registry: "ghcr.io", RepoTemplate: "https://github.com/{repo}"},
		{Registry: "registry.company.kz", RepoTemplate: "github.com/{org}/{image_name}", Org: "company"},
	})

	cases := []struct {
		image  string
		want   string
		mapped bool
	}{
		{"ghcr.io/mechta-market/promo-sync:latest", "https://github.com/mechta-market/promo-sync", true},
		{"ghcr.io/rendau/kusec:latest", "https://github.com/rendau/kusec", true},
		// несколько образов из одной репы: путь пакета длиннее пути репозитория
		{"ghcr.io/rendau/loom/server:latest", "https://github.com/rendau/loom", true},
		{"ghcr.io/rendau/loom/artifact:latest", "https://github.com/rendau/loom", true},
		{"registry.company.kz/team/payments-api:a3f9c21", "https://github.com/company/payments-api", true},
		{"postgres:17-bullseye", "", false},
		{"redis:alpine", "", false},
		{"quay.io/prometheus/node-exporter:v1.8.0", "", false},
	}

	for _, c := range cases {
		ref, err := imageref.Parse(c.image)
		require.NoError(t, err)
		got, mapped := mapper.RepoUrl(ref)
		assert.Equal(t, c.mapped, mapped, c.image)
		assert.Equal(t, c.want, got, c.image)
	}
}
