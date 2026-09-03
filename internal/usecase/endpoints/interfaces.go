package endpoints

import (
	"context"

	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	svcproxyModel "github.com/mechta-market/pulse/internal/service/svcproxy/model"
	"github.com/mechta-market/pulse/internal/usecase/endpoints/model"
)

type EndpointsI interface {
	Call(ctx context.Context, req *model.CallReq) (*model.CallResult, error)
}

// ports

type svcServiceI interface {
	GetOrSuggest(ctx context.Context, name string) (*svcModel.Main, error)
}

type workloadServiceI interface {
	List(ctx context.Context, pars *workloadModel.ListReq) ([]*workloadModel.Main, int64, error)
}

// CallerI — GET к ручке внутри кластера (прямой вызов либо через API-прокси k8s).
type CallerI interface {
	Get(ctx context.Context, namespace, service string, port int, path string, query map[string]string, maxBytes int64) (*svcproxyModel.Response, error)
}
