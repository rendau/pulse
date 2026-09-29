package service

import (
	"context"

	k8sModel "github.com/rendau/pulse/internal/service/k8s/model"
)

// K8sI — свой под и Service его namespace: по ним видно, какие Service ведут на сам pulse.
type K8sI interface {
	SelfPod(ctx context.Context) (*k8sModel.Pod, error)
	ListServices(ctx context.Context, namespace string) ([]k8sModel.Service, error)
}
