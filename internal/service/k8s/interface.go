package k8s

import (
	"context"

	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
)

// Client — read-only доступ к кластеру: только get/list.
type Client interface {
	// ListWorkloads возвращает Deployment/StatefulSet/DaemonSet/CronJob всех namespace'ов,
	// кроме исключённых.
	ListWorkloads(ctx context.Context) ([]k8sModel.Workload, error)
	// ListPods возвращает поды namespace'а по label-селектору (формат «k=v,k2=v2»).
	ListPods(ctx context.Context, namespace, selector string) ([]k8sModel.Pod, error)
	Ping(ctx context.Context) error
}
