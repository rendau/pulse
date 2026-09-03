package loki

import (
	"context"
	"time"

	lokiModel "github.com/mechta-market/pulse/internal/service/loki/model"
)

// Client — read-only доступ к Loki HTTP API.
type Client interface {
	// QueryRange выполняет LogQL-запрос за интервал; limit — максимум строк (backward: новые первыми).
	QueryRange(ctx context.Context, query string, start, end time.Time, limit int) ([]lokiModel.Stream, error)
	Ping(ctx context.Context) error
}
