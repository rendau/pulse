package alertmanager

import "context"

// Client — read-only доступ к Alertmanager HTTP API.
type Client interface {
	Ping(ctx context.Context) error
}
