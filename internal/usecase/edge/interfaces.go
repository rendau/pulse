package edge

import (
	"context"
	"time"

	lokiModel "github.com/rendau/pulse/internal/service/loki/model"
	"github.com/rendau/pulse/internal/usecase/edge/model"
)

type EdgeI interface {
	// Traffic — сводка внешних запросов за окно по адресам клиентов.
	Traffic(ctx context.Context, req *model.TrafficReq) (*model.Traffic, error)
	// ClientActivity — сжатая хронология запросов одного адреса.
	ClientActivity(ctx context.Context, req *model.ActivityReq) (*model.Activity, error)
}

// ports

// LokiI экспортирован: источник опционален, композиционный корень передаёт nil.
type LokiI interface {
	QueryRange(ctx context.Context, query string, start, end time.Time, limit int) ([]lokiModel.Stream, error)
	QueryVector(ctx context.Context, query string, at time.Time) ([]lokiModel.Sample, error)
}
