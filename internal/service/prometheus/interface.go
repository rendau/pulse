package prometheus

import (
	"context"
	"time"

	prometheusModel "github.com/rendau/pulse/internal/service/prometheus/model"
)

// Client — read-only доступ к Prometheus HTTP API.
type Client interface {
	// Query — instant-запрос на момент at (нулевое время — сейчас).
	Query(ctx context.Context, promql string, at time.Time) ([]prometheusModel.Sample, error)
	// QueryRange — range-запрос.
	QueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) ([]prometheusModel.Series, error)
	Ping(ctx context.Context) error
}
