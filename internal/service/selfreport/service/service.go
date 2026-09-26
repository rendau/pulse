// Package service — самоотчёт сервиса: ручка состояния (манифест) на готовых подах его
// workload'ов, худший под, зависимости — только объявленные в манифесте, объекты — только из
// domain. Общий для снапшота сервиса и здоровья кластера.
package service

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	"github.com/mechta-market/pulse/internal/constant"
	snapshotModel "github.com/mechta-market/pulse/internal/domain/snapshot/model"
	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
	selfstatusModel "github.com/mechta-market/pulse/internal/service/selfstatus/model"
	svcproxyModel "github.com/mechta-market/pulse/internal/service/svcproxy/model"
)

const (
	// podsLimit — сколько готовых подов опрашивать: состояние у каждого пода своё
	podsLimit = 3
	// staleAfter — отчёт старше: фоновая проверка в сервисе, похоже, остановилась
	staleAfter = 5 * time.Minute
)

// statusRank — чем больше, тем хуже.
var statusRank = map[string]int{selfstatusModel.StatusOk: 0, selfstatusModel.StatusDegraded: 1, selfstatusModel.StatusDown: 2}

type k8sI interface {
	ListPods(ctx context.Context, namespace, selector string) ([]k8sModel.Pod, error)
}

type selfStatusI interface {
	Get(ctx context.Context, target svcproxyModel.PodTarget) (*selfstatusModel.Status, error)
}

type Service struct {
	k8s  k8sI
	self selfStatusI
	now  func() time.Time
}

func New(k8s k8sI, self selfStatusI) *Service {
	return &Service{k8s: k8s, self: self, now: time.Now}
}

// Report — самоотчёт сервиса (nil — у его workload'ов нет манифеста или ни один под не ответил) и
// ошибки источников по ходу.
func (s *Service) Report(ctx context.Context, service *svcModel.Main, workloads []*workloadModel.Main) (*snapshotModel.SelfReport, []snapshotModel.SourceError) {
	withManifest := lo.Filter(workloads, func(w *workloadModel.Main, _ int) bool {
		return w.Selector != "" && w.Manifest.Port > 0 &&
			(w.Manifest.Status == workloadModel.ManifestOk || w.Manifest.Status == workloadModel.ManifestPartial)
	})
	if len(withManifest) == 0 {
		return nil, nil
	}

	var mu sync.Mutex
	var failures []snapshotModel.SourceError
	fail := func(source string, err error) {
		mu.Lock()
		defer mu.Unlock()
		failures = append(failures, snapshotModel.SourceError{Source: source, Message: err.Error()})
	}

	var targets []svcproxyModel.PodTarget
	for _, w := range withManifest {
		pods, err := s.k8s.ListPods(ctx, w.Namespace, w.Selector)
		if err != nil {
			fail(constant.SourceK8s, fmt.Errorf("pods %s/%s: %w", w.Namespace, w.Name, err))
			continue
		}
		pods = lo.Filter(pods, func(p k8sModel.Pod, _ int) bool { return p.Ready && p.IP != "" })
		sort.Slice(pods, func(i, j int) bool { return pods[i].Name < pods[j].Name })
		for _, p := range lo.Slice(pods, 0, podsLimit) {
			targets = append(targets, svcproxyModel.PodTarget{Namespace: p.Namespace, Pod: p.Name, IP: p.IP, Port: w.Manifest.Port})
		}
	}

	type answer struct {
		pod    string
		status *selfstatusModel.Status
	}
	var answers []answer
	eg, egCtx := errgroup.WithContext(ctx)
	for _, target := range targets {
		eg.Go(func() error {
			status, err := s.self.Get(egCtx, target)
			switch {
			case err != nil:
				fail(constant.SourceServiceStatus, err)
			case status != nil:
				mu.Lock()
				answers = append(answers, answer{pod: target.Pod, status: status})
				mu.Unlock()
			}
			return nil
		})
	}
	_ = eg.Wait()
	if len(answers) == 0 {
		return nil, failures
	}

	// худший под; при равенстве — по имени (детерминированно)
	sort.Slice(answers, func(i, j int) bool {
		ri, rj := statusRank[answers[i].status.Status], statusRank[answers[j].status.Status]
		return ri > rj || (ri == rj && answers[i].pod < answers[j].pod)
	})
	worst := answers[0]

	return &snapshotModel.SelfReport{
		Status:       worst.status.Status,
		Pod:          worst.pod,
		Pods:         len(answers),
		CheckedAt:    worst.status.CheckedAt,
		Stale:        !worst.status.CheckedAt.IsZero() && s.now().Sub(worst.status.CheckedAt) > staleAfter,
		Dependencies: selfDependencies(service.Metadata.Dependencies, worst.status.Dependencies),
		Gauges: lo.Map(worst.status.Gauges, func(g selfstatusModel.Gauge, _ int) snapshotModel.SelfGauge {
			return snapshotModel.SelfGauge{Id: g.Id, Title: g.Title, Value: g.Value, Time: g.Time, Unit: g.Unit, Status: g.Status}
		}),
		Entities: selfEntities(service.Metadata.Domain, worst.status.Entities),
	}, failures
}

// selfDependencies — зависимости из манифеста с состоянием из отчёта: необъявленные в
// манифесте записи отчёта не показываются (пропускается только объявленное).
func selfDependencies(declared []svcModel.Dependency, reported []selfstatusModel.Dependency) []snapshotModel.SelfDependency {
	byId := lo.SliceToMap(reported, func(d selfstatusModel.Dependency) (string, selfstatusModel.Dependency) { return d.Id, d })
	return lo.Map(declared, func(d svcModel.Dependency, _ int) snapshotModel.SelfDependency {
		dep := snapshotModel.SelfDependency{Id: d.Id, Kind: d.Kind, Target: d.Target, Critical: d.Critical, Affects: d.Affects}
		if r, ok := byId[d.Id]; ok {
			dep.Status, dep.LatencyMs, dep.Message = r.Status, r.LatencyMs, r.Message
		}
		return dep
	})
}

// selfEntities — объекты из domain манифеста со счётчиками из отчёта: смысл статуса и порог
// застревания — из domain; объекты, которых нет в domain, не показываются.
func selfEntities(domain *svcModel.Domain, reported []selfstatusModel.Entity) []snapshotModel.SelfEntity {
	if domain == nil {
		return nil
	}
	var result []snapshotModel.SelfEntity
	for _, r := range reported {
		declared, ok := lo.Find(domain.Entities, func(e svcModel.Entity) bool { return e.Name == r.Name })
		if !ok {
			continue
		}
		entity := snapshotModel.SelfEntity{Name: r.Name, Status: r.Status, Created1h: r.Created1h, Finished1h: r.Finished1h}
		for _, st := range r.Statuses {
			status := snapshotModel.SelfEntityStatus{Name: st.Name, Count: st.Count, Stuck: st.Stuck, Oldest: st.Oldest}
			if d, ok := lo.Find(declared.Statuses, func(s svcModel.EntityStatus) bool { return s.Name == st.Name }); ok {
				status.Meaning, status.StuckAfter = d.Meaning, d.StuckAfter
			}
			entity.Statuses = append(entity.Statuses, status)
		}
		result = append(result, entity)
	}
	return result
}
