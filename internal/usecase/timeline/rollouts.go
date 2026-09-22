package timeline

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	"github.com/mechta-market/pulse/internal/constant"
	eventModel "github.com/mechta-market/pulse/internal/domain/event/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
)

// serviceRollout — выкатка с причиной и сервисом её workload'а.
type serviceRollout struct {
	eventModel.Rollout
	service string
}

// perWorkloadMax — до стольких workload'ов в namespace их объекты (ReplicaSet'ы, поды) читаются
// по селектору каждого; больше — одним списком на namespace (scope=cluster: сотни workload'ов
// упираются в лимит запросов к API-серверу и в дедлайн).
const perWorkloadMax = 5

// forWorkloads запускает perWorkload для каждого workload'а с селектором, а для namespace'ов
// с числом workload'ов больше perWorkloadMax — perNamespace один раз на namespace.
func forWorkloads(ctx context.Context, workloads []*workloadModel.Main,
	perWorkload func(ctx context.Context, w *workloadModel.Main), perNamespace func(ctx context.Context, namespace string, group []*workloadModel.Main)) {
	workloads = lo.Filter(workloads, func(w *workloadModel.Main, _ int) bool { return w.Selector != "" })

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(podsConcurrent)
	for namespace, group := range lo.GroupBy(workloads, func(w *workloadModel.Main) string { return w.Namespace }) {
		if len(group) > perWorkloadMax {
			eg.Go(func() error {
				perNamespace(egCtx, namespace, group)
				return nil
			})
			continue
		}
		for _, w := range group {
			eg.Go(func() error {
				perWorkload(egCtx, w)
				return nil
			})
		}
	}
	_ = eg.Wait()
}

// rollouts — выкатки Deployment'ов за окно с причиной из ревизий шаблона пода (ReplicaSet):
// смена configmap/secret (reloader) и ручной рестарт. Ошибки — по запросу, без прерывания.
func (u *Usecase) rollouts(ctx context.Context, workloads []*workloadModel.Main, since time.Time, addError func(string, error)) []serviceRollout {
	deployments := lo.Filter(workloads, func(w *workloadModel.Main, _ int) bool {
		return w.Kind == constant.WorkloadKindDeployment
	})

	var mu sync.Mutex
	result := make([]serviceRollout, 0)
	collect := func(w *workloadModel.Main, replicaSets []k8sModel.ReplicaSet) {
		revisions := lo.FilterMap(replicaSets, func(rs k8sModel.ReplicaSet, _ int) (eventModel.PodTemplateRevision, bool) {
			return eventModel.PodTemplateRevision{Name: rs.Name, Revision: rs.Revision, CreatedAt: rs.CreatedAt, Annotations: rs.TemplateAnnotations},
				rs.OwnerKind == constant.WorkloadKindDeployment && rs.OwnerName == w.Name
		})
		found := lo.Map(u.events.Rollouts(w.Namespace+"/"+w.Name, revisions, since), func(r eventModel.Rollout, _ int) serviceRollout {
			return serviceRollout{Rollout: r, service: w.ServiceName}
		})
		mu.Lock()
		result = append(result, found...)
		mu.Unlock()
	}

	forWorkloads(ctx, deployments, func(ctx context.Context, w *workloadModel.Main) {
		replicaSets, err := u.k8s.ListReplicaSets(ctx, w.Namespace, w.Selector)
		if err != nil {
			addError(constant.SourceK8s, fmt.Errorf("replicasets %s/%s: %w", w.Namespace, w.Name, err))
			return
		}
		collect(w, replicaSets)
	}, func(ctx context.Context, namespace string, group []*workloadModel.Main) {
		replicaSets, err := u.k8s.ListReplicaSets(ctx, namespace, "")
		if err != nil {
			addError(constant.SourceK8s, fmt.Errorf("replicasets %s: %w", namespace, err))
			return
		}
		for _, w := range group {
			collect(w, replicaSets)
		}
	})

	return result
}
