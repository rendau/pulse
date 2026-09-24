package service

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/mechta-market/pulse/internal/constant"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
)

const requestTimeout = 30 * time.Second

// Service — клиент кластера. Ошибка инициализации не фатальна: сервис обязан
// стартовать при недоступном источнике, поэтому она откладывается до первого
// вызова и видна в Ping.
type Service struct {
	clientset         kubernetes.Interface
	initErr           error
	excludeNamespaces map[string]struct{}
}

// New строит клиент: kubeconfig пустой — in-cluster конфиг, иначе файл kubeconfig
// с опциональным контекстом (локальная разработка).
func New(kubeconfig, kubeContext string, excludeNamespaces []string) *Service {
	s := &Service{
		excludeNamespaces: lo.SliceToMap(excludeNamespaces, func(ns string) (string, struct{}) {
			return ns, struct{}{}
		}),
	}

	restConf, err := buildRestConfig(kubeconfig, kubeContext)
	if err != nil {
		s.initErr = fmt.Errorf("kubernetes config: %w", err)
		slog.Warn("kubernetes client is not initialized", "error", s.initErr)
		return s
	}

	restConf.Timeout = requestTimeout
	restConf.QPS = 20
	restConf.Burst = 40

	s.clientset, err = kubernetes.NewForConfig(restConf)
	if err != nil {
		s.initErr = fmt.Errorf("kubernetes.NewForConfig: %w", err)
		slog.Warn("kubernetes client is not initialized", "error", s.initErr)
	}

	return s
}

func buildRestConfig(kubeconfig, kubeContext string) (*rest.Config, error) {
	if kubeconfig == "" {
		return rest.InClusterConfig()
	}

	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig},
		&clientcmd.ConfigOverrides{CurrentContext: kubeContext},
	).ClientConfig()
}

func (s *Service) Ping(ctx context.Context) error {
	if s.initErr != nil {
		return s.initErr
	}

	if _, err := s.clientset.Discovery().ServerVersion(); err != nil {
		return fmt.Errorf("discovery.ServerVersion: %w", err)
	}

	return nil
}

func (s *Service) ListWorkloads(ctx context.Context) ([]k8sModel.Workload, error) {
	if s.initErr != nil {
		return nil, s.initErr
	}

	result := make([]k8sModel.Workload, 0, 200)
	listOpts := metav1.ListOptions{}

	deployments, err := s.clientset.AppsV1().Deployments(metav1.NamespaceAll).List(ctx, listOpts)
	if err != nil {
		return nil, fmt.Errorf("Deployments.List: %w", err)
	}
	for _, d := range deployments.Items {
		if s.excluded(d.Namespace) {
			continue
		}
		result = append(result, k8sModel.Workload{
			Kind:            constant.WorkloadKindDeployment,
			Namespace:       d.Namespace,
			Name:            d.Name,
			ReplicasDesired: lo.FromPtr(d.Spec.Replicas),
			Selector:        selectorString(d.Spec.Selector),
			Containers:      encodeContainers(d.Spec.Template.Spec.Containers),
			ConfigRefs:      configRefs(d.Spec.Template.Spec),
			CreatedAt:       d.CreationTimestamp.Time,
		})
	}

	statefulSets, err := s.clientset.AppsV1().StatefulSets(metav1.NamespaceAll).List(ctx, listOpts)
	if err != nil {
		return nil, fmt.Errorf("StatefulSets.List: %w", err)
	}
	for _, st := range statefulSets.Items {
		if s.excluded(st.Namespace) {
			continue
		}
		result = append(result, k8sModel.Workload{
			Kind:            constant.WorkloadKindStatefulSet,
			Namespace:       st.Namespace,
			Name:            st.Name,
			ReplicasDesired: lo.FromPtr(st.Spec.Replicas),
			Selector:        selectorString(st.Spec.Selector),
			Containers:      encodeContainers(st.Spec.Template.Spec.Containers),
			ConfigRefs:      configRefs(st.Spec.Template.Spec),
			CreatedAt:       st.CreationTimestamp.Time,
		})
	}

	daemonSets, err := s.clientset.AppsV1().DaemonSets(metav1.NamespaceAll).List(ctx, listOpts)
	if err != nil {
		return nil, fmt.Errorf("DaemonSets.List: %w", err)
	}
	for _, ds := range daemonSets.Items {
		if s.excluded(ds.Namespace) {
			continue
		}
		result = append(result, k8sModel.Workload{
			Kind:            constant.WorkloadKindDaemonSet,
			Namespace:       ds.Namespace,
			Name:            ds.Name,
			ReplicasDesired: ds.Status.DesiredNumberScheduled,
			Selector:        selectorString(ds.Spec.Selector),
			Containers:      encodeContainers(ds.Spec.Template.Spec.Containers),
			ConfigRefs:      configRefs(ds.Spec.Template.Spec),
			CreatedAt:       ds.CreationTimestamp.Time,
		})
	}

	cronJobs, err := s.clientset.BatchV1().CronJobs(metav1.NamespaceAll).List(ctx, listOpts)
	if err != nil {
		return nil, fmt.Errorf("CronJobs.List: %w", err)
	}
	for _, cj := range cronJobs.Items {
		if s.excluded(cj.Namespace) {
			continue
		}
		result = append(result, k8sModel.Workload{
			Kind:       constant.WorkloadKindCronJob,
			Namespace:  cj.Namespace,
			Name:       cj.Name,
			Containers: encodeContainers(cj.Spec.JobTemplate.Spec.Template.Spec.Containers),
			ConfigRefs: configRefs(cj.Spec.JobTemplate.Spec.Template.Spec),
			CreatedAt:  cj.CreationTimestamp.Time,
		})
	}

	return result, nil
}

func (s *Service) ListPods(ctx context.Context, namespace, selector string) ([]k8sModel.Pod, error) {
	if s.initErr != nil {
		return nil, s.initErr
	}

	pods, err := s.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, fmt.Errorf("Pods.List: %w", err)
	}

	return lo.Map(pods.Items, encodePod), nil
}

func (s *Service) excluded(namespace string) bool {
	_, ok := s.excludeNamespaces[namespace]
	return ok
}

func selectorString(sel *metav1.LabelSelector) string {
	if sel == nil {
		return ""
	}
	labelSel, err := metav1.LabelSelectorAsSelector(sel)
	if err != nil {
		return ""
	}
	return labelSel.String()
}

// encoders k8s → contract

func encodeContainers(containers []corev1.Container) []k8sModel.Container {
	return lo.Map(containers, func(c corev1.Container, _ int) k8sModel.Container {
		result := k8sModel.Container{Name: c.Name, Image: c.Image, Env: lo.Map(c.Env, encodeEnvVar)}
		for _, from := range c.EnvFrom {
			if from.ConfigMapRef != nil {
				result.EnvFromConfigMaps = append(result.EnvFromConfigMaps, from.ConfigMapRef.Name)
			}
		}
		return result
	})
}

// configRefs — имена configmap/secret шаблона пода, отсортированные и без повторов.
func configRefs(spec corev1.PodSpec) []string {
	refs := make([]string, 0, 4)
	for _, c := range append(append([]corev1.Container{}, spec.InitContainers...), spec.Containers...) {
		for _, from := range c.EnvFrom {
			if from.ConfigMapRef != nil {
				refs = append(refs, from.ConfigMapRef.Name)
			}
			if from.SecretRef != nil {
				refs = append(refs, from.SecretRef.Name)
			}
		}
		for _, env := range c.Env {
			switch {
			case env.ValueFrom == nil:
			case env.ValueFrom.ConfigMapKeyRef != nil:
				refs = append(refs, env.ValueFrom.ConfigMapKeyRef.Name)
			case env.ValueFrom.SecretKeyRef != nil:
				refs = append(refs, env.ValueFrom.SecretKeyRef.Name)
			}
		}
	}
	for _, v := range spec.Volumes {
		switch {
		case v.ConfigMap != nil:
			refs = append(refs, v.ConfigMap.Name)
		case v.Secret != nil:
			refs = append(refs, v.Secret.SecretName)
		case v.Projected != nil:
			for _, src := range v.Projected.Sources {
				if src.ConfigMap != nil {
					refs = append(refs, src.ConfigMap.Name)
				}
				if src.Secret != nil {
					refs = append(refs, src.Secret.Name)
				}
			}
		}
	}
	refs = lo.Uniq(lo.Compact(refs))
	slices.Sort(refs)
	return refs
}

// encodeEnvVar: значения из secret не читаются никогда (Р7), только факт ссылки.
func encodeEnvVar(v corev1.EnvVar, _ int) k8sModel.EnvVar {
	result := k8sModel.EnvVar{Name: v.Name, Value: v.Value}
	if v.ValueFrom == nil {
		return result
	}
	switch {
	case v.ValueFrom.SecretKeyRef != nil:
		result.FromSecret = true
	case v.ValueFrom.ConfigMapKeyRef != nil:
		result.ConfigMapRef = v.ValueFrom.ConfigMapKeyRef.Name + "/" + v.ValueFrom.ConfigMapKeyRef.Key
	}
	return result
}

func encodePod(p corev1.Pod, _ int) k8sModel.Pod {
	result := k8sModel.Pod{
		Namespace:  p.Namespace,
		Name:       p.Name,
		Phase:      string(p.Status.Phase),
		NodeName:   p.Spec.NodeName,
		Labels:     p.Labels,
		Containers: lo.Map(p.Status.ContainerStatuses, encodePodContainer),
	}

	if p.Status.StartTime != nil {
		result.StartedAt = p.Status.StartTime.Time
	}

	for _, cond := range p.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			result.Ready = true
		}
	}

	result.Restarts = lo.SumBy(result.Containers, func(c k8sModel.PodContainer) int32 { return c.Restarts })

	return result
}

func encodePodContainer(cs corev1.ContainerStatus, _ int) k8sModel.PodContainer {
	result := k8sModel.PodContainer{
		Name:     cs.Name,
		Image:    cs.Image,
		ImageID:  cs.ImageID,
		Ready:    cs.Ready,
		Restarts: cs.RestartCount,
	}

	switch {
	case cs.State.Running != nil:
		result.State = "running"
	case cs.State.Waiting != nil:
		result.State = "waiting"
		result.Reason = cs.State.Waiting.Reason
	case cs.State.Terminated != nil:
		result.State = "terminated"
		result.Reason = cs.State.Terminated.Reason
		result.TerminatedAt = cs.State.Terminated.FinishedAt.Time
	}

	if cs.LastTerminationState.Terminated != nil {
		result.LastTerminationReason = cs.LastTerminationState.Terminated.Reason
		result.LastTerminatedAt = cs.LastTerminationState.Terminated.FinishedAt.Time
	}

	return result
}
