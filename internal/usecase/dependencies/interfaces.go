package dependencies

import (
	"context"

	dependencyModel "github.com/mechta-market/pulse/internal/domain/dependency/model"
	snapshotModel "github.com/mechta-market/pulse/internal/domain/snapshot/model"
	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
	"github.com/mechta-market/pulse/internal/usecase/dependencies/model"
)

type DependenciesI interface {
	Graph(ctx context.Context, req *model.GraphReq) (*model.Graph, error)
}

// ports

type svcServiceI interface {
	GetOrSuggest(ctx context.Context, name string) (*svcModel.Main, error)
	List(ctx context.Context, pars *svcModel.ListReq) ([]*svcModel.Main, int64, error)
}

type workloadServiceI interface {
	List(ctx context.Context, pars *workloadModel.ListReq) ([]*workloadModel.Main, int64, error)
}

type dependencyServiceI interface {
	List(ctx context.Context, pars *dependencyModel.ListReq) ([]*dependencyModel.Main, int64, error)
}

type k8sClientI interface {
	ListPods(ctx context.Context, namespace, selector string) ([]k8sModel.Pod, error)
}

type rulesServiceI interface {
	ComputeHealth(snap *snapshotModel.Snapshot, podsUnavailable bool) string
}
