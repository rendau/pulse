package service

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
)

const roleLabelPrefix = "node-role.kubernetes.io/"

func (s *Service) ListNodes(ctx context.Context) ([]k8sModel.Node, error) {
	if s.initErr != nil {
		return nil, s.initErr
	}

	nodes, err := s.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("Nodes.List: %w", err)
	}

	result := make([]k8sModel.Node, 0, len(nodes.Items))
	for _, n := range nodes.Items {
		node := k8sModel.Node{
			Name:           n.Name,
			Unschedulable:  n.Spec.Unschedulable,
			KubeletVersion: n.Status.NodeInfo.KubeletVersion,
			CPUMillis:      n.Status.Allocatable.Cpu().MilliValue(),
			MemoryBytes:    n.Status.Allocatable.Memory().Value(),
			CreatedAt:      n.CreationTimestamp.Time,
		}
		for _, cond := range n.Status.Conditions {
			switch {
			case cond.Type == corev1.NodeReady:
				node.Ready = cond.Status == corev1.ConditionTrue
			case cond.Status == corev1.ConditionTrue:
				node.Pressures = append(node.Pressures, string(cond.Type))
			}
		}
		for label := range n.Labels {
			if role, ok := strings.CutPrefix(label, roleLabelPrefix); ok && role != "" {
				node.Roles = append(node.Roles, role)
			}
		}
		result = append(result, node)
	}

	return result, nil
}
