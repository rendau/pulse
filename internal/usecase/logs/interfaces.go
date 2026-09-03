package logs

import (
	"context"
	"time"

	logsModel "github.com/mechta-market/pulse/internal/domain/logs/model"
	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	lokiModel "github.com/mechta-market/pulse/internal/service/loki/model"
	"github.com/mechta-market/pulse/internal/usecase/logs/model"
)

type LogsI interface {
	Query(ctx context.Context, req *model.QueryReq) (*model.QueryResult, error)
	// TopErrors — верхние error-паттерны сервиса за окно; для снапшота.
	TopErrors(ctx context.Context, service *svcModel.Main, workloads []*workloadModel.Main, window time.Duration, top int) ([]logsModel.Pattern, error)
}

// ports

type svcServiceI interface {
	GetOrSuggest(ctx context.Context, name string) (*svcModel.Main, error)
}

type workloadServiceI interface {
	List(ctx context.Context, pars *workloadModel.ListReq) ([]*workloadModel.Main, int64, error)
}

// LokiI экспортирован: источник опционален, композиционный корень передаёт nil.
type LokiI interface {
	QueryRange(ctx context.Context, query string, start, end time.Time, limit int) ([]lokiModel.Stream, error)
}

type patternsServiceI interface {
	DetectLevel(line string) string
	Aggregate(lines []logsModel.Line, top int) []logsModel.Pattern
}
