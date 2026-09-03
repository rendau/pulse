package catalog

import (
	"context"

	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
	"github.com/mechta-market/pulse/internal/usecase/catalog/model"
)

type CatalogI interface {
	Resolve(ctx context.Context, query string) (*model.ResolveResult, error)
	List(ctx context.Context, pars *model.ListReq) ([]*model.ServiceSummary, int64, error)
	Info(ctx context.Context, name string) (*model.ServiceInfo, error)
}

// ports

type svcServiceI interface {
	List(ctx context.Context, pars *svcModel.ListReq) ([]*svcModel.Main, int64, error)
	GetOrSuggest(ctx context.Context, name string) (*svcModel.Main, error)
	Resolve(ctx context.Context, query string) ([]*svcModel.Candidate, error)
}

type workloadServiceI interface {
	List(ctx context.Context, pars *workloadModel.ListReq) ([]*workloadModel.Main, int64, error)
}

type k8sClientI interface {
	ListPods(ctx context.Context, namespace, selector string) ([]k8sModel.Pod, error)
}
