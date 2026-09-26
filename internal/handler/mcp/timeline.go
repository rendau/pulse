package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rendau/pulse/internal/handler/mcp/dto"
	usecaseTimelineModel "github.com/rendau/pulse/internal/usecase/timeline/model"
)

const (
	getTimelineDescription = `Все изменения на одной оси времени, новые первыми: деплои (смена образа), коммиты, изменения ` +
		`конфигурации, срабатывания алертов, масштабирование, рестарты и OOM. ` +
		`Выбирай для «что изменилось перед тем, как сломалось», «что деплоили сегодня»; scope=cluster — по всему кластеру. ` +
		`Второй по важности после get_service_snapshot. Не подходит для текущего состояния и логов.`

	getChangesDescription = `Детализация изменений одного сервиса: коммиты с авторами, что смержено, но ещё не в проде ` +
		`(на сколько коммитов отстаёт), история деплоев с образами, правки конфигурации в kusec по ключам с авторами, ` +
		`применение в кластер и неприменённые правки (unsynced_config); секреты маскированы. ` +
		`Выбирай для «покажи, что именно поменяли», «какая версия в проде и что не выкачено». Требует точное имя сервиса.`
)

func (h *Handler) GetTimeline(ctx context.Context, _ *mcp.CallToolRequest, req dto.GetTimelineReq) (*mcp.CallToolResult, dto.TimelineRep, error) {
	win, err := parseWindowDefault(req.Window, timelineDefaultWindow)
	if err != nil {
		return nil, dto.TimelineRep{}, toolError(err)
	}

	services := req.Services
	if req.Service != "" {
		services = append([]string{req.Service}, services...)
	}

	result, err := h.timeline.Timeline(ctx, &usecaseTimelineModel.TimelineReq{Services: services, Scope: req.Scope, Window: win})
	if err != nil {
		return nil, dto.TimelineRep{}, toolError(err)
	}
	return nil, dto.EncodeTimelineRep(result), nil
}

func (h *Handler) GetChanges(ctx context.Context, _ *mcp.CallToolRequest, req dto.GetChangesReq) (*mcp.CallToolResult, dto.ChangesRep, error) {
	win, err := parseWindowDefault(req.Window, timelineDefaultWindow)
	if err != nil {
		return nil, dto.ChangesRep{}, toolError(err)
	}

	result, err := h.timeline.Changes(ctx, req.Service, win)
	if err != nil {
		return nil, dto.ChangesRep{}, toolError(err)
	}
	return nil, dto.EncodeChangesRep(result), nil
}
