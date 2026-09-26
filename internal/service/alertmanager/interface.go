package alertmanager

import (
	"context"

	alertmanagerModel "github.com/rendau/pulse/internal/service/alertmanager/model"
)

// Client — read-only доступ к Alertmanager HTTP API.
type Client interface {
	// ListAlerts возвращает текущие алерты (активные и подавленные).
	ListAlerts(ctx context.Context) ([]alertmanagerModel.Alert, error)
	Ping(ctx context.Context) error
}
