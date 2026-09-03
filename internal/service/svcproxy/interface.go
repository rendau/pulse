package svcproxy

import (
	"context"

	svcproxyModel "github.com/mechta-market/pulse/internal/service/svcproxy/model"
)

// Client — GET к ручке сервиса внутри кластера (ClusterIP через DNS name.namespace.svc).
// Только GET (Р5): никаких мутирующих методов.
type Client interface {
	Get(ctx context.Context, namespace, service string, port int, path string, query map[string]string, maxBytes int64) (*svcproxyModel.Response, error)
}
