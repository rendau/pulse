package registry

import "context"

// Client — read-only доступ к OCI-registry (Docker Registry HTTP API v2).
type Client interface {
	// GetImageLabels возвращает labels из конфига образа. ref — «host/path@sha256:…»
	// или «host/path:tag». Результат по digest кэшируется навсегда: digest неизменяем.
	GetImageLabels(ctx context.Context, ref string) (map[string]string, error)
	// Ping проверяет доступность API registry (/v2/ отвечает 200 или 401).
	Ping(ctx context.Context, host string) error
}
