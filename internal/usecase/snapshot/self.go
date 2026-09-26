package snapshot

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
	// selfPodsLimit — сколько готовых подов опрашивать: состояние у каждого пода своё
	selfPodsLimit = 3
	// selfStaleAfter — отчёт старше: фоновая проверка в сервисе, похоже, остановилась
	selfStaleAfter = 5 * time.Minute
)

// statusRank — чем больше, тем хуже.
var statusRank = map[string]int{selfstatusModel.StatusOk: 0, selfstatusModel.StatusDegraded: 1, selfstatusModel.StatusDown: 2}

// selfReport — что сервис сообщает о себе сам: ручка состояния на подах workload'ов, где
// найден манифест. Показывается худший под; зависимости — только объявленные в манифесте.
func (c *collector) selfReport(ctx context.Context) {
	if c.u.self == nil {
		return
	}
	withManifest := lo.Filter(c.workloads, func(w *workloadModel.Main, _ int) bool {
		return w.Selector != "" && w.Manifest.Port > 0 &&
			(w.Manifest.Status == workloadModel.ManifestOk || w.Manifest.Status == workloadModel.ManifestPartial)
	})
	if len(withManifest) == 0 {
		return
	}

	var targets []svcproxyModel.PodTarget
	for _, w := range withManifest {
		pods, err := c.u.k8s.ListPods(ctx, w.Namespace, w.Selector)
		if err != nil {
			c.addError(constant.SourceK8s, fmt.Errorf("pods %s/%s: %w", w.Namespace, w.Name, err))
			continue
		}
		pods = lo.Filter(pods, func(p k8sModel.Pod, _ int) bool { return p.Ready && p.IP != "" })
		sort.Slice(pods, func(i, j int) bool { return pods[i].Name < pods[j].Name })
		for _, p := range lo.Slice(pods, 0, selfPodsLimit) {
			targets = append(targets, svcproxyModel.PodTarget{Namespace: p.Namespace, Pod: p.Name, IP: p.IP, Port: w.Manifest.Port})
		}
	}

	type answer struct {
		pod    string
		status *selfstatusModel.Status
	}
	var mu sync.Mutex
	var answers []answer
	eg, egCtx := errgroup.WithContext(ctx)
	for _, target := range targets {
		eg.Go(func() error {
			status, err := c.u.self.Get(egCtx, target)
			switch {
			case err != nil:
				c.addError(constant.SourceServiceStatus, err)
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
		return
	}

	// худший под; при равенстве — по имени (детерминированно)
	sort.Slice(answers, func(i, j int) bool {
		ri, rj := statusRank[answers[i].status.Status], statusRank[answers[j].status.Status]
		return ri > rj || (ri == rj && answers[i].pod < answers[j].pod)
	})
	worst := answers[0]

	report := &snapshotModel.SelfReport{
		Status:       worst.status.Status,
		Pod:          worst.pod,
		Pods:         len(answers),
		CheckedAt:    worst.status.CheckedAt,
		Stale:        !worst.status.CheckedAt.IsZero() && c.now.Sub(worst.status.CheckedAt) > selfStaleAfter,
		Dependencies: selfDependencies(c.service.Metadata.Dependencies, worst.status.Dependencies),
		Gauges: lo.Map(worst.status.Gauges, func(g selfstatusModel.Gauge, _ int) snapshotModel.SelfGauge {
			return snapshotModel.SelfGauge{Id: g.Id, Title: g.Title, Value: g.Value, Time: g.Time, Unit: g.Unit, Status: g.Status}
		}),
		Entities: selfEntities(c.service.Metadata.Domain, worst.status.Entities),
	}

	c.mu.Lock()
	c.snap.Self = report
	c.mu.Unlock()
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
