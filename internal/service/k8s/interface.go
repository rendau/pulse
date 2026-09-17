package k8s

import (
	"context"
	"time"

	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
)

// Client — read-only доступ к кластеру: только get/list.
type Client interface {
	// ListWorkloads возвращает Deployment/StatefulSet/DaemonSet/CronJob всех namespace'ов,
	// кроме исключённых.
	ListWorkloads(ctx context.Context) ([]k8sModel.Workload, error)
	// ListPods возвращает поды namespace'а по label-селектору (формат «k=v,k2=v2»).
	ListPods(ctx context.Context, namespace, selector string) ([]k8sModel.Pod, error)
	// ListConfigMaps возвращает configmap'ы namespace'а (пусто — все namespace'ы).
	ListConfigMaps(ctx context.Context, namespace string) ([]k8sModel.ConfigMap, error)
	// ListServices возвращает k8s Services (пусто — все namespace'ы).
	ListServices(ctx context.Context, namespace string) ([]k8sModel.Service, error)
	// ListNodes возвращает ноды кластера с условиями готовности и давления.
	ListNodes(ctx context.Context) ([]k8sModel.Node, error)
	// ProxyGet выполняет GET к k8s Service через API-сервер (services/proxy). Для локальной
	// разработки, когда ClusterIP недоступен напрямую.
	ProxyGet(ctx context.Context, namespace, service string, port int, path string, query map[string]string) ([]byte, error)
	// ListReplicaSets возвращает ReplicaSet'ы namespace'а по label-селектору (ревизии Deployment'ов).
	ListReplicaSets(ctx context.Context, namespace, selector string) ([]k8sModel.ReplicaSet, error)
	// ListEvents возвращает события namespace'а не старше since.
	ListEvents(ctx context.Context, namespace string, since time.Time) ([]k8sModel.Event, error)
	Ping(ctx context.Context) error
}
