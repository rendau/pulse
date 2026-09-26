package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rendau/pulse/internal/handler/mcp/dto"
)

const getClusterHealthDescription = `Здоровье кластера в целом: ноды (готовность, давление), поды в проблемных состояниях и pending ` +
	`по всему кластеру, Warning-события по причинам, инфраструктурные алерты, загрузка CPU/памяти в сравнении с вчера, ` +
	`ошибки в логах всего кластера по сервисам (log_errors), сервисы, которые сами сообщают о проблеме (self_reported), ` +
	`публичные приложения API-gateway с проблемой — сбои, backend не отвечает, сломан скрипт маршрута, медленно, пропал трафик (public_apps). ` +
	`Выбирай для «у нас всё лежит или только платежи», «что с кластером», «есть ли ошибки в логах» без сервиса, «всё ли в порядке снаружи», когда деградировало несколько сервисов сразу. ` +
	`Не подходит для диагностики одного сервиса — для этого get_service_snapshot.`

func (h *Handler) GetClusterHealth(ctx context.Context, _ *mcp.CallToolRequest, req dto.GetClusterHealthReq) (*mcp.CallToolResult, dto.ClusterHealthRep, error) {
	win, err := parseWindow(req.Window)
	if err != nil {
		return nil, dto.ClusterHealthRep{}, toolError(err)
	}

	result, err := h.cluster.Health(ctx, win)
	if err != nil {
		return nil, dto.ClusterHealthRep{}, toolError(err)
	}
	return nil, dto.EncodeClusterHealthRep(result), nil
}
