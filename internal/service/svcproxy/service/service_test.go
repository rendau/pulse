package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	svcproxyModel "github.com/rendau/pulse/internal/service/svcproxy/model"
)

// Текст ошибки — без query: там бывает значение, подставленное вместо токена.
func TestGetPod_ErrorWithoutQuery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := New().GetPod(ctx, svcproxyModel.PodTarget{IP: "127.0.0.1", Port: 1}, "/diag/orders",
		map[string]string{"phone": "77011234567"}, nil, 1024)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "77011234567")
	assert.Contains(t, err.Error(), "127.0.0.1:1/diag/orders")
}
