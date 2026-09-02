package prometheus

import "context"

// Client — read-only доступ к Prometheus HTTP API.
type Client interface {
	Ping(ctx context.Context) error
}
