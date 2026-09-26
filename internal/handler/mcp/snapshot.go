package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rendau/pulse/internal/errs"
	"github.com/rendau/pulse/internal/handler/mcp/dto"
	usecaseSnapshotModel "github.com/rendau/pulse/internal/usecase/snapshot/model"
	"github.com/rendau/pulse/internal/util/window"
)

const (
	getServiceSnapshotDescription = `Главный инструмент диагностики: за один вызов собирает активные алерты, состояние подов, ` +
		`метрики в сравнении с часом назад и вчера, события кластера (OOM, рестарты, ошибки образа) и вычисленный health. ` +
		`Выбирай ПЕРВЫМ для «что с сервисом X», «почему не работает», «всё ли в порядке». ` +
		`Не подходит для истории изменений и логов. Требует точное имя сервиса; window — окно событий (по умолчанию 1h).`

	queryMetricsDescription = `Drill-down по метрике: временной ряд за окно. Предпочитай metric_id из карточки/снапшота сервиса; ` +
		`произвольный promql — только когда нужной метрики нет (лимиты: 20 серий, 200 точек). ` +
		`Выбирай после get_service_snapshot, когда нужна динамика («когда началось падение»), а не текущее значение. ` +
		`Возвращает серии точек [ts, value].`
)

func (h *Handler) GetServiceSnapshot(ctx context.Context, _ *mcp.CallToolRequest, req dto.GetServiceSnapshotReq) (*mcp.CallToolResult, dto.SnapshotRep, error) {
	win, err := parseWindow(req.Window)
	if err != nil {
		return nil, dto.SnapshotRep{}, toolError(err)
	}

	result, err := h.snapshot.Snapshot(ctx, req.Service, win)
	if err != nil {
		return nil, dto.SnapshotRep{}, toolError(err)
	}
	return nil, dto.EncodeSnapshotRep(result), nil
}

func (h *Handler) QueryMetrics(ctx context.Context, _ *mcp.CallToolRequest, req dto.QueryMetricsReq) (*mcp.CallToolResult, dto.QueryMetricsRep, error) {
	win, err := parseWindow(req.Window)
	if err != nil {
		return nil, dto.QueryMetricsRep{}, toolError(err)
	}

	var step time.Duration
	if req.Step != "" {
		if step, err = time.ParseDuration(req.Step); err != nil || step <= 0 {
			return nil, dto.QueryMetricsRep{}, toolError(fmt.Errorf("%w: step %q: expected Go duration like 30s, 5m", errs.InvalidRequest, req.Step))
		}
	}

	result, err := h.snapshot.QueryMetrics(ctx, &usecaseSnapshotModel.QueryMetricsReq{
		Service:  req.Service,
		MetricId: req.MetricId,
		PromQL:   req.PromQL,
		Window:   win,
		Step:     step,
	})
	if err != nil {
		return nil, dto.QueryMetricsRep{}, toolError(err)
	}
	return nil, dto.EncodeQueryMetricsRep(result), nil
}

const timelineDefaultWindow = 24 * time.Hour

func parseWindow(s string) (time.Duration, error) {
	return parseWindowDefault(s, window.Default)
}

func parseWindowDefault(s string, def time.Duration) (time.Duration, error) {
	win, err := window.Parse(s, def, window.Max)
	if err != nil {
		return 0, fmt.Errorf("%w: %s", errs.InvalidRequest, err)
	}
	return win, nil
}
