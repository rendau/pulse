package service

import (
	"context"
	"fmt"

	"github.com/samber/lo"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
)

func (s *Service) ListJobs(ctx context.Context, namespace string) ([]k8sModel.Job, error) {
	if s.initErr != nil {
		return nil, s.initErr
	}

	jobs, err := s.clientset.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("Jobs.List: %w", err)
	}

	return lo.Map(jobs.Items, encodeJob), nil
}

func encodeJob(j batchv1.Job, _ int) k8sModel.Job {
	return k8sModel.Job{
		Namespace: j.Namespace,
		Name:      j.Name,
		Labels:    j.Labels,
		Images:    lo.Map(j.Spec.Template.Spec.Containers, func(c corev1.Container, _ int) string { return c.Image }),
	}
}
