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
	"github.com/mechta-market/pulse/internal/usecase/timeline/model"
	"github.com/mechta-market/pulse/internal/util/redact"
)

// serviceRollout — выкатка с причиной и сервисом её workload'а.
type serviceRollout struct {
	eventModel.Rollout
	service string
}

// rollouts — выкатки Deployment'ов за окно с причиной из ревизий шаблона пода (ReplicaSet):
// смена configmap/secret (reloader) и ручной рестарт. Ошибки — по workload'у, без прерывания.
func (u *Usecase) rollouts(ctx context.Context, workloads []*workloadModel.Main, since time.Time, addError func(string, error)) []serviceRollout {
	var mu sync.Mutex
	result := make([]serviceRollout, 0)

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(podsConcurrent)
	for _, w := range workloads {
		if w.Kind != constant.WorkloadKindDeployment || w.Selector == "" {
			continue
		}
		eg.Go(func() error {
			replicaSets, err := u.k8s.ListReplicaSets(egCtx, w.Namespace, w.Selector)
			if err != nil {
				addError(constant.SourceK8s, fmt.Errorf("replicasets %s/%s: %w", w.Namespace, w.Name, err))
				return nil
			}
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
			return nil
		})
	}
	_ = eg.Wait()

	return result
}

// encodeRolloutConfigChange — выкатка reloader'а как изменение конфигурации: имя объекта и
// отпечатки содержимого; у secret отпечатки тоже скрыты (Р7).
func encodeRolloutConfigChange(v serviceRollout) (model.ConfigChange, bool) {
	if v.Cause != eventModel.RolloutCauseConfigReload {
		return model.ConfigChange{}, false
	}
	change := model.ConfigChange{
		TS: v.TS, Source: model.ConfigChangeSourceReloader, Workload: v.Workload,
		Kind: v.ConfigKind, Key: v.ConfigName, OldValue: v.PrevHash, NewValue: v.Hash,
	}
	if v.ConfigKind == "secret" {
		change.OldValue, change.NewValue = redact.Secret(), redact.Secret()
	}
	return change, true
}
