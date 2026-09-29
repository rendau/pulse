// Package service — самоотчёт сервиса: ручка состояния (манифест) через k8s Service каждого его
// workload'а (отвечает любой под за Service), худший из ответов, зависимости — только объявленные
// в манифесте, объекты — только из domain. Общий для снапшота сервиса и здоровья кластера.
package service

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	"github.com/rendau/pulse/internal/constant"
	snapshotModel "github.com/rendau/pulse/internal/domain/snapshot/model"
	svcModel "github.com/rendau/pulse/internal/domain/svc/model"
	workloadModel "github.com/rendau/pulse/internal/domain/workload/model"
	selfstatusModel "github.com/rendau/pulse/internal/service/selfstatus/model"
	svcproxyModel "github.com/rendau/pulse/internal/service/svcproxy/model"
)

// staleAfter — отчёт старше: фоновая проверка в сервисе, похоже, остановилась
const staleAfter = 5 * time.Minute

// statusRank — чем больше, тем хуже.
var statusRank = map[string]int{selfstatusModel.StatusOk: 0, selfstatusModel.StatusDegraded: 1, selfstatusModel.StatusDown: 2}

type selfStatusI interface {
	Get(ctx context.Context, target svcproxyModel.ServiceTarget) (*selfstatusModel.Status, error)
}

type Service struct {
	self selfStatusI
	now  func() time.Time
}

func New(self selfStatusI) *Service {
	return &Service{self: self, now: time.Now}
}

// Report — самоотчёт сервиса (nil — у его workload'ов нет манифеста или никто не ответил) и
// ошибки источников по ходу.
func (s *Service) Report(ctx context.Context, service *svcModel.Main, workloads []*workloadModel.Main) (*snapshotModel.SelfReport, []snapshotModel.SourceError) {
	// один запрос на Service: два workload'а за одним Service — один и тот же ответ
	targets := lo.Uniq(lo.FilterMap(workloads, func(w *workloadModel.Main, _ int) (svcproxyModel.ServiceTarget, bool) {
		return svcproxyModel.ServiceTarget{Namespace: w.Namespace, Service: w.Manifest.Service, Port: w.Manifest.Port}, w.Manifest.Callable()
	}))
	if len(targets) == 0 {
		return nil, nil
	}

	var mu sync.Mutex
	var failures []snapshotModel.SourceError
	type answer struct {
		target svcproxyModel.ServiceTarget
		status *selfstatusModel.Status
	}
	var answers []answer
	eg, egCtx := errgroup.WithContext(ctx)
	for _, target := range targets {
		eg.Go(func() error {
			status, err := s.self.Get(egCtx, target)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				failures = append(failures, snapshotModel.SourceError{Source: constant.SourceServiceStatus, Message: err.Error()})
			case status != nil:
				answers = append(answers, answer{target: target, status: status})
			}
			return nil
		})
	}
	_ = eg.Wait()
	if len(answers) == 0 {
		return nil, failures
	}

	// худший ответ; при равенстве — по имени Service (детерминированно)
	sort.Slice(answers, func(i, j int) bool {
		ri, rj := statusRank[answers[i].status.Status], statusRank[answers[j].status.Status]
		return ri > rj || (ri == rj && answers[i].target.Service < answers[j].target.Service)
	})
	worst := answers[0].status

	return &snapshotModel.SelfReport{
		Status:       worst.Status,
		Answers:      len(answers),
		CheckedAt:    worst.CheckedAt,
		Stale:        !worst.CheckedAt.IsZero() && s.now().Sub(worst.CheckedAt) > staleAfter,
		Dependencies: selfDependencies(service.Metadata.Dependencies, worst.Dependencies),
		Gauges: lo.Map(worst.Gauges, func(g selfstatusModel.Gauge, _ int) snapshotModel.SelfGauge {
			return snapshotModel.SelfGauge{Id: g.Id, Title: g.Title, Value: g.Value, Time: g.Time, Unit: g.Unit, Status: g.Status}
		}),
		Entities: selfEntities(service.Metadata.Domain, worst.Entities),
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
