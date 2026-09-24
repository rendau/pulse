package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mechta-market/pulse/internal/handler/mcp/dto"
	usecaseLogsModel "github.com/mechta-market/pulse/internal/usecase/logs/model"
)

const queryLogsDescription = `Логи сервиса из Loki, по умолчанию агрегированные в паттерны: тысяча одинаковых ошибок ` +
	`приходит одной строкой со счётчиком, примером и временем первого/последнего появления. ` +
	`Выбирай после get_service_snapshot, чтобы узнать, НА ЧЁМ именно падает сервис (level=error), или проверить конкретную ошибку (pattern). ` +
	`mode=raw — последние строки (≤100) для деталей; workload — только один workload сервиса (у паттерна видно, из каких он). ` +
	`Телефоны, email и номера карт в строках маскированы. Без Loki — из Kubernetes (source=kubernetes: только живые поды, хвост). ` +
	`Не подходит для метрик и событий кластера. Требует точное имя сервиса.`

func (h *Handler) QueryLogs(ctx context.Context, _ *mcp.CallToolRequest, req dto.QueryLogsReq) (*mcp.CallToolResult, dto.QueryLogsRep, error) {
	win, err := parseWindow(req.Window)
	if err != nil {
		return nil, dto.QueryLogsRep{}, toolError(err)
	}

	result, err := h.logs.Query(ctx, &usecaseLogsModel.QueryReq{
		Service:  req.Service,
		Level:    req.Level,
		Pattern:  req.Pattern,
		Window:   win,
		Mode:     req.Mode,
		Limit:    req.Limit,
		Workload: req.Workload,
	})
	if err != nil {
		return nil, dto.QueryLogsRep{}, toolError(err)
	}
	return nil, dto.EncodeQueryLogsRep(result), nil
}
