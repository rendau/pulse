package endpoints

import (
	"context"

	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
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

type k8sClientI interface {
	ListPods(ctx context.Context, namespace, selector string) ([]k8sModel.Pod, error)
}

// CallerI — GET прямо в под (по IP; локально — через API-прокси k8s).
type CallerI interface {
	GetPod(ctx context.Context, target svcproxyModel.PodTarget, path string, query, headers map[string]string, maxBytes int64) (*svcproxyModel.Response, error)
}

// PiiI — персональные данные токенами: в ответе ручки — токены, токен в параметре — настоящее
// значение (только в исходящий запрос).
type PiiI interface {
	Tokenize(kind, value string) string
	Text(s string) string
	Resolve(kind, value string) (string, error)
}
