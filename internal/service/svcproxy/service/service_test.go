package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	svcproxyModel "github.com/rendau/pulse/internal/service/svcproxy/model"
)

// Адрес — DNS-имя Service внутри кластера и его порт: отвечает любой под за Service.
func TestServiceUrl(t *testing.T) {
	target := svcproxyModel.ServiceTarget{Namespace: "prod", Service: "ocenter", Port: 3003}
	assert.Equal(t, "http://ocenter.prod.svc:3003/.well-known/pulse", serviceUrl(target, "/.well-known/pulse", nil))
	assert.Equal(t, "http://ocenter.prod.svc:3003/diag/orders?id=234115", serviceUrl(target, "/diag/orders", map[string]string{"id": "234115"}))
}

// Текст ошибки — без query: там бывает значение, подставленное вместо токена.
func TestSendRequest_ErrorWithoutQuery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := New().sendRequest(ctx, "http://127.0.0.1:1/diag/orders?phone=77011234567", nil, 1024)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "77011234567")
	assert.Contains(t, err.Error(), "127.0.0.1:1/diag/orders")
}
