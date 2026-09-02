package service

import (
	"context"
	"time"

	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
)

type k8sClientI interface {
	ListWorkloads(ctx context.Context) ([]k8sModel.Workload, error)
	ListPods(ctx context.Context, namespace, selector string) ([]k8sModel.Pod, error)
}

type githubClientI interface {
	GetFileContent(ctx context.Context, repoUrl, path string) ([]byte, bool, error)
}

type registryClientI interface {
	GetImageLabels(ctx context.Context, ref string) (map[string]string, error)
}

type svcServiceI interface {
	List(ctx context.Context, pars *svcModel.ListReq) ([]*svcModel.Main, int64, error)
	UpdateOrCreate(ctx context.Context, obj *svcModel.Edit) error
	DeleteStale(ctx context.Context, before time.Time) ([]string, error)
}

type workloadServiceI interface {
	UpdateOrCreateMany(ctx context.Context, objs []*workloadModel.Edit) error
	DeleteStale(ctx context.Context, cluster string, before time.Time) (int64, error)
}
