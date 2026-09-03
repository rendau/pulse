package service

import (
	"context"
	"fmt"

	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
)

func (s *Service) ListConfigMaps(ctx context.Context, namespace string) ([]k8sModel.ConfigMap, error) {
	if s.initErr != nil {
		return nil, s.initErr
	}

	items, err := s.clientset.CoreV1().ConfigMaps(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("ConfigMaps.List: %w", err)
	}

	result := make([]k8sModel.ConfigMap, 0, len(items.Items))
	for _, cm := range items.Items {
		if s.excluded(cm.Namespace) {
			continue
		}
		result = append(result, k8sModel.ConfigMap{Namespace: cm.Namespace, Name: cm.Name, Data: cm.Data})
	}

	return result, nil
}

func (s *Service) ListServices(ctx context.Context, namespace string) ([]k8sModel.Service, error) {
	if s.initErr != nil {
		return nil, s.initErr
	}

	items, err := s.clientset.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("Services.List: %w", err)
	}

	result := make([]k8sModel.Service, 0, len(items.Items))
	for _, svc := range items.Items {
		if s.excluded(svc.Namespace) {
			continue
		}
		result = append(result, k8sModel.Service{
			Namespace: svc.Namespace,
			Name:      svc.Name,
			Selector:  svc.Spec.Selector,
			ClusterIP: svc.Spec.ClusterIP,
			Ports: lo.Map(svc.Spec.Ports, func(p corev1.ServicePort, _ int) k8sModel.ServicePort {
				return k8sModel.ServicePort{Name: p.Name, Port: p.Port, TargetPort: p.TargetPort.String()}
			}),
		})
	}

	return result, nil
}
