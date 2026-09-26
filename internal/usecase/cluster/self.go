package cluster

import (
	"context"
	"fmt"
	"sort"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	clusterModel "github.com/mechta-market/pulse/internal/domain/cluster/model"
	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	selfstatusModel "github.com/mechta-market/pulse/internal/service/selfstatus/model"
)

// selfReportsParallel — сколько сервисов опрашивать одновременно.
const selfReportsParallel = 8

// selfReports — сервисы с манифестом, которые сами сообщают о проблеме: самоотчёт не ok или
// устарел. Ошибки опроса отдельных сервисов — не в errors кластера: сервис, который не ответил,
// виден в его снапшоте.
func (c *collector) selfReports(ctx context.Context) {
	if c.u.self == nil || c.u.svc == nil {
		return
	}
	byService := lo.GroupBy(lo.Filter(c.workloads, func(w *workloadModel.Main, _ int) bool {
		return w.ServiceName != "" && w.Manifest.Port > 0 &&
			(w.Manifest.Status == workloadModel.ManifestOk || w.Manifest.Status == workloadModel.ManifestPartial)
	}), func(w *workloadModel.Main) string { return w.ServiceName })
	if len(byService) == 0 {
		return
	}
	services, _, err := c.u.svc.List(ctx, &svcModel.ListReq{Names: lo.Keys(byService)})
	if err != nil {
		c.addError("catalog", fmt.Errorf("svc.List: %w", err))
		return
	}

	var reports []clusterModel.ServiceSelfReport
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(selfReportsParallel)
	for _, s := range services {
		eg.Go(func() error {
			report, _ := c.u.self.Report(egCtx, s, byService[s.Name])
			if report == nil || (report.Status == selfstatusModel.StatusOk && !report.Stale) {
				return nil
			}
			c.mu.Lock()
			reports = append(reports, clusterModel.ServiceSelfReport{Service: s.Name, Report: report, Hints: c.u.baseline.SelfHints(report, c.now)})
			c.mu.Unlock()
			return nil
		})
	}
	_ = eg.Wait()

	sort.Slice(reports, func(i, j int) bool { return reports[i].Service < reports[j].Service })
	c.mu.Lock()
	c.h.SelfReported = reports
	c.mu.Unlock()
}
