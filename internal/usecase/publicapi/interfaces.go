package publicapi

import (
	"context"
	"time"

	dependencyModel "github.com/mechta-market/pulse/internal/domain/dependency/model"
	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	prometheusModel "github.com/mechta-market/pulse/internal/service/prometheus/model"
	rutoModel "github.com/mechta-market/pulse/internal/service/ruto/model"
	"github.com/mechta-market/pulse/internal/usecase/publicapi/model"
)

type PublicApiI interface {
	PublicApi(ctx context.Context, service string, window time.Duration) (*model.PublicApi, error)
}

// ports

type svcServiceI interface {
	GetOrSuggest(ctx context.Context, name string) (*svcModel.Main, error)
}

type dependencyServiceI interface {
	List(ctx context.Context, pars *dependencyModel.ListReq) ([]*dependencyModel.Main, int64, error)
}

// RutoI и PrometheusI экспортированы: источники опциональны, композиционный корень передаёт nil.
type RutoI interface {
	GetSnapshot(ctx context.Context) (*rutoModel.Snapshot, error)
}

type PrometheusI interface {
	Query(ctx context.Context, promql string, at time.Time) ([]prometheusModel.Sample, error)
}
