package timeline

import (
	"context"
	"time"

	deployModel "github.com/mechta-market/pulse/internal/domain/deploy/model"
	eventModel "github.com/mechta-market/pulse/internal/domain/event/model"
	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	githubModel "github.com/mechta-market/pulse/internal/service/github/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
	kusecModel "github.com/mechta-market/pulse/internal/service/kusec/model"
	prometheusModel "github.com/mechta-market/pulse/internal/service/prometheus/model"
	"github.com/mechta-market/pulse/internal/usecase/timeline/model"
)

type TimelineI interface {
	Timeline(ctx context.Context, req *model.TimelineReq) (*model.TimelineResult, error)
	Changes(ctx context.Context, service string, window time.Duration) (*model.ChangesResult, error)
}

// ports

type svcServiceI interface {
	GetOrSuggest(ctx context.Context, name string) (*svcModel.Main, error)
	List(ctx context.Context, pars *svcModel.ListReq) ([]*svcModel.Main, int64, error)
}

type workloadServiceI interface {
	List(ctx context.Context, pars *workloadModel.ListReq) ([]*workloadModel.Main, int64, error)
}

type deployServiceI interface {
	List(ctx context.Context, pars *deployModel.ListReq) ([]*deployModel.Main, int64, error)
}

type k8sClientI interface {
	ListPods(ctx context.Context, namespace, selector string) ([]k8sModel.Pod, error)
	ListEvents(ctx context.Context, namespace string, since time.Time) ([]k8sModel.Event, error)
}

type githubClientI interface {
	ListCommits(ctx context.Context, repoUrl string, since, until time.Time, limit int) ([]githubModel.Commit, error)
	CompareCommits(ctx context.Context, repoUrl, base string) (*githubModel.Comparison, error)
}

// KusecI и PrometheusI экспортированы: источники опциональны, композиционный корень передаёт nil.
type KusecI interface {
	ListChanges(ctx context.Context, service string, since, until time.Time) ([]kusecModel.Change, error)
}

type PrometheusI interface {
	QueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) ([]prometheusModel.Series, error)
}

type eventServiceI interface {
	FromCluster(e eventModel.ClusterEvent, service string) (eventModel.Event, bool)
	FromTermination(t eventModel.ContainerTermination, service string) (eventModel.Event, bool)
	FromDeploy(d *deployModel.Main) eventModel.Event
}

type rulesServiceI interface {
	AlertMatches(labels map[string]string, names []string) bool
	AlertSeverity(s string) string
}
