package loki

import "context"

// Client — read-only доступ к Loki HTTP API.
type Client interface {
	Ping(ctx context.Context) error
}
