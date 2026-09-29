package svcproxy

import (
	"context"

	svcproxyModel "github.com/rendau/pulse/internal/service/svcproxy/model"
)

// Client — GET к ручке сервиса внутри кластера через его k8s Service: манифест, ручка состояния
// и диагностические ручки из манифеста. Только GET (Р5): никаких мутирующих методов.
type Client interface {
	// GetService — GET в k8s Service (отвечает любой под за ним), без редиректов; headers —
	// служебные заголовки pulse. Свой Service (ведёт на под самого pulse) — через localhost:
	// hairpin через VIP своего Service проходит не во всех сетях.
	GetService(ctx context.Context, target svcproxyModel.ServiceTarget, path string, query, headers map[string]string, maxBytes int64) (*svcproxyModel.Response, error)
}
