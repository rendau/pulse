package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k8sModel "github.com/rendau/pulse/internal/service/k8s/model"
)

// serviceAccountNamespaceFile — namespace пода: kubelet монтирует его вместе с токеном
// ServiceAccount, по которому pulse ходит в API из кластера.
const serviceAccountNamespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

// SelfPod — под, в котором работает pulse: имя — hostname контейнера (в k8s это имя пода),
// namespace — из ServiceAccount. Вне кластера (kubeconfig) своего пода нет — nil.
func (s *Service) SelfPod(ctx context.Context) (*k8sModel.Pod, error) {
	if !s.inCluster {
		return nil, nil
	}
	if s.initErr != nil {
		return nil, s.initErr
	}

	namespace, name, err := selfIdentity(serviceAccountNamespaceFile, os.Hostname)
	if err != nil {
		return nil, fmt.Errorf("own pod: %w", err)
	}
	pod, err := s.clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("Pods.Get(%s/%s): %w", namespace, name, err)
	}

	return new(encodePod(*pod, 0)), nil
}

// selfIdentity — namespace и имя своего пода.
func selfIdentity(namespaceFile string, hostname func() (string, error)) (string, string, error) {
	raw, err := os.ReadFile(namespaceFile)
	if err != nil {
		return "", "", fmt.Errorf("namespace: %w", err)
	}
	name, err := hostname()
	if err != nil {
		return "", "", fmt.Errorf("hostname: %w", err)
	}
	namespace := strings.TrimSpace(string(raw))
	if namespace == "" || name == "" {
		return "", "", errors.New("empty namespace or hostname")
	}
	return namespace, name, nil
}
