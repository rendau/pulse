package logs

import (
	"context"
	"time"

	logsModel "github.com/mechta-market/pulse/internal/domain/logs/model"
	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
	lokiModel "github.com/mechta-market/pulse/internal/service/loki/model"
	"github.com/mechta-market/pulse/internal/usecase/logs/model"
)

type LogsI interface {
	Query(ctx context.Context, req *model.QueryReq) (*model.QueryResult, error)
	// TopErrors — верхние error-паттерны сервиса за окно; для снапшота.
	TopErrors(ctx context.Context, service *svcModel.Main, workloads []*workloadModel.Main, window time.Duration, top int) ([]logsModel.Pattern, error)
	// ClusterErrors — ошибки по всем логам кластера (top сервисов); для здоровья кластера.
	ClusterErrors(ctx context.Context, window time.Duration, top int) (*logsModel.ClusterErrors, error)
}

// ports

type svcServiceI interface {
	GetOrSuggest(ctx context.Context, name string) (*svcModel.Main, error)
}

type workloadServiceI interface {
	List(ctx context.Context, pars *workloadModel.ListReq) ([]*workloadModel.Main, int64, error)
}

type k8sClientI interface {
	ListPods(ctx context.Context, namespace, selector string) ([]k8sModel.Pod, error)
	PodLogs(ctx context.Context, namespace, pod, container string, since time.Time, tail int64, previous bool) ([]k8sModel.LogLine, error)
}

// LokiI экспортирован: источник опционален, композиционный корень передаёт nil.
type LokiI interface {
	QueryRange(ctx context.Context, query string, start, end time.Time, limit int) ([]lokiModel.Stream, error)
	QueryVector(ctx context.Context, query string, at time.Time) ([]lokiModel.Sample, error)
}

// PiiI — персональные данные токенами: в строках логов вместо телефонов и email — токены,
// токен в шаблоне поиска — поиск настоящего значения.
type PiiI interface {
	Text(s string) string
	SearchPattern(pattern string) (string, error)
}

type patternsServiceI interface {
	DetectLevel(line string) string
	Aggregate(lines []logsModel.Line, top int) []logsModel.Pattern
}
