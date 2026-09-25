package svcproxy

import (
	"context"

	svcproxyModel "github.com/mechta-market/pulse/internal/service/svcproxy/model"
)

// Client — GET к ручке сервиса внутри кластера: через k8s Service (ClusterIP по DNS
// name.namespace.svc) или прямо в под. Только GET (Р5): никаких мутирующих методов.
type Client interface {
	Get(ctx context.Context, namespace, service string, port int, path string, query map[string]string, maxBytes int64) (*svcproxyModel.Response, error)
	// GetPod — GET прямо в под (IP:порт), без редиректов; headers — служебные заголовки pulse.
	GetPod(ctx context.Context, target svcproxyModel.PodTarget, path string, query, headers map[string]string, maxBytes int64) (*svcproxyModel.Response, error)
}
