package endpoints

import (
	"context"

	svcModel "github.com/rendau/pulse/internal/domain/svc/model"
	workloadModel "github.com/rendau/pulse/internal/domain/workload/model"
	svcproxyModel "github.com/rendau/pulse/internal/service/svcproxy/model"
	"github.com/rendau/pulse/internal/usecase/endpoints/model"
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

// CallerI — GET в k8s Service (отвечает любой под за ним; локально — через API-прокси k8s).
type CallerI interface {
	GetService(ctx context.Context, target svcproxyModel.ServiceTarget, path string, query, headers map[string]string, maxBytes int64) (*svcproxyModel.Response, error)
}

// PiiI — персональные данные: карты и учётные данные вырезаются, персональный параметр
// приводится к одному виду; телефоны и email от модели прячет агент (по personal_fields).
type PiiI interface {
	Text(s string) string
	Value(kind, value string) string
	Normalize(kind, value string) (string, error)
}
