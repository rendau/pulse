package service

import (
	"context"
	"time"

	dependencyModel "github.com/rendau/pulse/internal/domain/dependency/model"
	deployModel "github.com/rendau/pulse/internal/domain/deploy/model"
	svcModel "github.com/rendau/pulse/internal/domain/svc/model"
	workloadModel "github.com/rendau/pulse/internal/domain/workload/model"
	githubModel "github.com/rendau/pulse/internal/service/github/model"
	k8sModel "github.com/rendau/pulse/internal/service/k8s/model"
	prometheusModel "github.com/rendau/pulse/internal/service/prometheus/model"
	rutoModel "github.com/rendau/pulse/internal/service/ruto/model"
	svcproxyModel "github.com/rendau/pulse/internal/service/svcproxy/model"
)

type k8sClientI interface {
	ListWorkloads(ctx context.Context) ([]k8sModel.Workload, error)
	ListPods(ctx context.Context, namespace, selector string) ([]k8sModel.Pod, error)
	ListConfigMaps(ctx context.Context, namespace string) ([]k8sModel.ConfigMap, error)
	ListServices(ctx context.Context, namespace string) ([]k8sModel.Service, error)
	ListJobs(ctx context.Context, namespace string) ([]k8sModel.Job, error)
}

type githubClientI interface {
	GetFileContent(ctx context.Context, repoUrl, path string) ([]byte, bool, error)
	RepoInfo(ctx context.Context, repoUrl string) (*githubModel.Repo, error)
	ResolveImageCommit(ctx context.Context, repoUrl, imagePath, digest string) (string, error)
	PackageRepoUrl(ctx context.Context, imagePath string) (string, error)
}

type registryClientI interface {
	GetImageLabels(ctx context.Context, ref string) (map[string]string, error)
}

// PodGetterI — GET прямо в под: манифест сервиса (svcproxy или pods/proxy локально).
type PodGetterI interface {
	GetPod(ctx context.Context, target svcproxyModel.PodTarget, path string, query, headers map[string]string, maxBytes int64) (*svcproxyModel.Response, error)
}

// PrometheusI экспортирован: источник опционален (порт /metrics пода — кандидат на манифест).
type PrometheusI interface {
	Query(ctx context.Context, promql string, at time.Time) ([]prometheusModel.Sample, error)
}

// RutoI экспортирован: источник опционален, композиционный корень передаёт nil.
type RutoI interface {
	GetSnapshot(ctx context.Context) (*rutoModel.Snapshot, error)
}

type svcServiceI interface {
	List(ctx context.Context, pars *svcModel.ListReq) ([]*svcModel.Main, int64, error)
	UpdateOrCreate(ctx context.Context, obj *svcModel.Edit) error
	DeleteStale(ctx context.Context, before time.Time) ([]string, error)
}

type workloadServiceI interface {
	List(ctx context.Context, pars *workloadModel.ListReq) ([]*workloadModel.Main, int64, error)
	UpdateOrCreateMany(ctx context.Context, objs []*workloadModel.Edit) error
	DeleteStale(ctx context.Context, cluster string, before time.Time) (int64, error)
}

type deployServiceI interface {
	Create(ctx context.Context, obj *deployModel.Edit) (int64, error)
}

type dependencyServiceI interface {
	ParseEndpoints(value string) []dependencyModel.Endpoint
	ClusterHost(host string) (string, string, bool)
	UpdateOrCreateMany(ctx context.Context, objs []*dependencyModel.Edit) error
	DeleteStale(ctx context.Context, cluster string, before time.Time) (int64, error)
}
