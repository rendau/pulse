package svcproxy

import (
	"context"

	svcproxyModel "github.com/rendau/pulse/internal/service/svcproxy/model"
)

// Client — GET к ручке сервиса внутри кластера прямо в под: манифест, ручка состояния и
// диагностические ручки из манифеста. Только GET (Р5): никаких мутирующих методов.
type Client interface {
	// GetPod — GET прямо в под (IP:порт), без редиректов; headers — служебные заголовки pulse.
	GetPod(ctx context.Context, target svcproxyModel.PodTarget, path string, query, headers map[string]string, maxBytes int64) (*svcproxyModel.Response, error)
}
