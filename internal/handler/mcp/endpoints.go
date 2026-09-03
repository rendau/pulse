package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mechta-market/pulse/internal/handler/mcp/dto"
	usecaseEndpointsModel "github.com/mechta-market/pulse/internal/usecase/endpoints/model"
)

const callServiceEndpointDescription = `Вызов диагностической ручки сервиса, объявленной в его service.yaml: предметные данные, ` +
	`которых нет в телеметрии — зависшие записи очереди, заявки в статусе, детали заказа. ` +
	`Список ручек и их параметры — в diagnostic_endpoints карточки get_service_info; вызвать можно только объявленный id ` +
	`с объявленными параметрами, только чтение. Не подходит для метрик, логов и состояния подов.`

func (h *Handler) CallServiceEndpoint(ctx context.Context, _ *mcp.CallToolRequest, req dto.CallServiceEndpointReq) (*mcp.CallToolResult, dto.CallServiceEndpointRep, error) {
	result, err := h.endpoints.Call(ctx, &usecaseEndpointsModel.CallReq{Service: req.Service, EndpointId: req.EndpointId, Params: req.Params})
	if err != nil {
		return nil, dto.CallServiceEndpointRep{}, toolError(err)
	}
	return nil, dto.EncodeCallServiceEndpointRep(result), nil
}
