package logs

import (
	"context"
	"time"

	logsModel "github.com/rendau/pulse/internal/domain/logs/model"
	svcModel "github.com/rendau/pulse/internal/domain/svc/model"
	workloadModel "github.com/rendau/pulse/internal/domain/workload/model"
	k8sModel "github.com/rendau/pulse/internal/service/k8s/model"
	lokiModel "github.com/rendau/pulse/internal/service/loki/model"
	"github.com/rendau/pulse/internal/usecase/logs/model"
)

type LogsI interface {
	Query(ctx context.Context, req *model.QueryReq) (*model.QueryResult, error)
	// TopErrors — верхние error-паттерны сервиса за окно; для снапшота.
	TopErrors(ctx context.Context, service *svcModel.Main, workloads []*workloadModel.Main, window time.Duration, top int) ([]logsModel.Pattern, error)
	// ClusterErrors — ошибки по всем логам кластера (top сервисов); для здоровья кластера.
	ClusterErrors(ctx context.Context, window time.Duration, top int) (*logsModel.ClusterErrors, error)
	// ServiceLines — строки сервиса по регэкспу за окно (все уровни); для здоровья кластера.
	ServiceLines(ctx context.Context, service, pattern string, window time.Duration, limit int) ([]logsModel.Line, error)
}

// ports

type svcServiceI interface {
	GetOrSuggest(ctx context.Context, name string) (*svcModel.Main, error)
	List(ctx context.Context, pars *svcModel.ListReq) ([]*svcModel.Main, int64, error)
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

// PiiI — строки логов без карт и учётных данных; телефон в шаблоне поиска — в любом написании.
type PiiI interface {
	Text(s string) string
	SearchPattern(pattern string) (literal, regex string)
}

type patternsServiceI interface {
	DetectLevel(line string) string
	Aggregate(lines []logsModel.Line, top int) []logsModel.Pattern
}
