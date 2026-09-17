package service

import (
	"context"
	"fmt"
	"strconv"

	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
)

const revisionAnnotation = "deployment.kubernetes.io/revision"

func (s *Service) ListReplicaSets(ctx context.Context, namespace, selector string) ([]k8sModel.ReplicaSet, error) {
	if s.initErr != nil {
		return nil, s.initErr
	}

	replicaSets, err := s.clientset.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, fmt.Errorf("ReplicaSets.List: %w", err)
	}

	return lo.Map(replicaSets.Items, encodeReplicaSet), nil
}

func encodeReplicaSet(rs appsv1.ReplicaSet, _ int) k8sModel.ReplicaSet {
	result := k8sModel.ReplicaSet{
		Namespace:           rs.Namespace,
		Name:                rs.Name,
		CreatedAt:           rs.CreationTimestamp.Time,
		TemplateAnnotations: rs.Spec.Template.Annotations,
	}
	result.Revision, _ = strconv.ParseInt(rs.Annotations[revisionAnnotation], 10, 64)
	if owner := metav1.GetControllerOf(&rs); owner != nil {
		result.OwnerKind, result.OwnerName = owner.Kind, owner.Name
	}
	return result
}
