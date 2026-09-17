package snapshot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/samber/lo"

	snapshotModel "github.com/mechta-market/pulse/internal/domain/snapshot/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	"github.com/mechta-market/pulse/internal/errs"
	prometheusModel "github.com/mechta-market/pulse/internal/service/prometheus/model"
	"github.com/mechta-market/pulse/internal/usecase/snapshot/model"
	"github.com/mechta-market/pulse/internal/util/window"
)

const (
	minStep       = 15 * time.Second
	defaultPoints = 100
)

// QueryMetrics — drill-down: ряд по metric_id сервиса или произвольному PromQL.
// Превышение лимитов (окно, число серий, точек) — ошибка с объяснением, как сузить
// запрос, а не усечённый результат.
func (u *Usecase) QueryMetrics(ctx context.Context, req *model.QueryMetricsReq) (*model.QueryMetricsResult, error) {
	if u.prometheus == nil {
		return nil, fmt.Errorf("%w: prometheus is not configured (PROMETHEUS_URL)", errs.ServiceNA)
	}
	if req.MetricId == "" && strings.TrimSpace(req.PromQL) == "" {
		return nil, fmt.Errorf("%w: metric_id or promql is required", errs.InvalidRequest)
	}

	service, err := u.svc.GetOrSuggest(ctx, req.Service)
	if err != nil {
		return nil, fmt.Errorf("svc.GetOrSuggest: %w", err)
	}

	win := req.Window
	if win <= 0 {
		win = window.Default
	}
	if win > u.conf.MaxWindow {
		return nil, fmt.Errorf("%w: window %s exceeds maximum %s; narrow the window", errs.InvalidRequest, win, u.conf.MaxWindow)
	}

	def := snapshotModel.MetricDef{Id: "promql", PromQL: strings.TrimSpace(req.PromQL)}
	if req.MetricId != "" {
		workloads, _, err := u.workload.List(ctx, &workloadModel.ListReq{ServiceName: new(service.Name)})
		if err != nil {
			return nil, fmt.Errorf("workload.List: %w", err)
		}
		defs := u.metricDefs(service, workloads, u.rutoApps(ctx, service.Name))
		found, ok := lo.Find(defs, func(d snapshotModel.MetricDef) bool { return d.Id == req.MetricId })
		if !ok {
			ids := lo.Map(defs, func(d snapshotModel.MetricDef, _ int) string { return d.Id })
			return nil, errs.ErrFull{Err: errs.ObjectNotFound, Desc: fmt.Sprintf("unknown metric_id %q for %s; available: %s",
				req.MetricId, service.Name, strings.Join(ids, ", "))}
		}
		def = found
	}

	step := req.Step
	if step <= 0 {
		step = win / defaultPoints
	}
	step = max(step.Round(time.Second), minStep)

	if points := int(win / step); points > u.conf.MaxPoints {
		return nil, fmt.Errorf("%w: %d points per series exceeds maximum %d; increase step to at least %s or narrow the window",
			errs.InvalidRequest, points, u.conf.MaxPoints, (win / time.Duration(u.conf.MaxPoints)).Round(time.Second))
	}

	end := time.Now().UTC()
	start := end.Add(-win)

	series, err := u.prometheus.QueryRange(ctx, def.PromQL, start, end, step)
	if err != nil {
		return nil, fmt.Errorf("prometheus.QueryRange: %w", err)
	}
	if len(series) > u.conf.MaxSeries {
		return nil, fmt.Errorf("%w: query returned %d series, maximum %d; add aggregation (sum by (...)) or narrow label filters",
			errs.InvalidRequest, len(series), u.conf.MaxSeries)
	}

	return &model.QueryMetricsResult{
		Service: service.Name,
		Def:     def,
		Start:   start,
		End:     end,
		Step:    step,
		Series:  lo.Map(series, encodeSeries),
	}, nil
}

func encodeSeries(v prometheusModel.Series, _ int) snapshotModel.Series {
	return snapshotModel.Series{
		Labels: v.Labels,
		Points: lo.Map(v.Points, func(p prometheusModel.Point, _ int) snapshotModel.Point {
			return snapshotModel.Point{TS: p.TS, Value: p.Value}
		}),
	}
}
