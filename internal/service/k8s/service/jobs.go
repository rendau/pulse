package service

import (
	"context"
	"fmt"

	"github.com/samber/lo"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
)

// ListJobs — Job'ы namespace'а (пусто — все, кроме исключённых из индекса).
func (s *Service) ListJobs(ctx context.Context, namespace string) ([]k8sModel.Job, error) {
	if s.initErr != nil {
		return nil, s.initErr
	}

	jobs, err := s.clientset.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("Jobs.List: %w", err)
	}

	return lo.FilterMap(jobs.Items, func(j batchv1.Job, _ int) (k8sModel.Job, bool) {
		return encodeJob(j), !s.excluded(j.Namespace)
	}), nil
}

func encodeJob(j batchv1.Job) k8sModel.Job {
	result := k8sModel.Job{
		Namespace:  j.Namespace,
		Name:       j.Name,
		Labels:     j.Labels,
		Containers: encodeContainers(j.Spec.Template.Spec.Containers),
		ConfigRefs: configRefs(j.Spec.Template.Spec),
		CreatedAt:  j.CreationTimestamp.Time,
	}
	if len(j.OwnerReferences) > 0 {
		result.OwnerKind = j.OwnerReferences[0].Kind
	}
	return result
}
