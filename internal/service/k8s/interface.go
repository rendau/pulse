package k8s

import (
	"context"
	"time"

	k8sModel "github.com/rendau/pulse/internal/service/k8s/model"
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
	// ProxyGetPod выполняет GET к поду через API-сервер (pods/proxy): статус ответа пода и тело.
	// Для локальной разработки, когда IP подов недоступны напрямую.
	ProxyGetPod(ctx context.Context, namespace, pod string, port int, path string, query, headers map[string]string, maxBytes int64) (int, []byte, error)
	// ListReplicaSets возвращает ReplicaSet'ы namespace'а по label-селектору (ревизии Deployment'ов).
	ListReplicaSets(ctx context.Context, namespace, selector string) ([]k8sModel.ReplicaSet, error)
	// ListEvents возвращает события namespace'а не старше since.
	ListEvents(ctx context.Context, namespace string, since time.Time) ([]k8sModel.Event, error)

	// ListJobs возвращает Job'ы namespace'а: метки, владелец и контейнеры шаблона пода.
	ListJobs(ctx context.Context, namespace string) ([]k8sModel.Job, error)
	// PodLogs — строки контейнера пода (pods/log); только у существующих подов.
	PodLogs(ctx context.Context, namespace, pod, container string, since time.Time, tail int64, previous bool) ([]k8sModel.LogLine, error)
	Ping(ctx context.Context) error
}
