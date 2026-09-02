package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLive_GetImageLabels ходит в реальный registry; включается переменной
// REGISTRY_LIVE_IMAGE=<ref> (например ghcr.io/actions/actions-runner:latest).
// Проверяет цепочку challenge → token → index → manifest → config.
func TestLive_GetImageLabels(t *testing.T) {
	ref := os.Getenv("REGISTRY_LIVE_IMAGE")
	if ref == "" {
		t.Skip("REGISTRY_LIVE_IMAGE is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s := New(map[string]string{"ghcr.io": os.Getenv("GITHUB_TOKEN")})

	labels, err := s.GetImageLabels(ctx, ref)
	require.NoError(t, err)
	t.Logf("labels: %v", labels)
	assert.NotNil(t, labels)

	require.NoError(t, s.Ping(ctx, "ghcr.io"))
}
