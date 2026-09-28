package mcp

import (
	"context"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rendau/pulse/internal/constant"
	"github.com/rendau/pulse/internal/handler/mcp/dto"
	usecaseEndpointsModel "github.com/rendau/pulse/internal/usecase/endpoints/model"
)

const callServiceEndpointDescription = `Вызов диагностической ручки, объявленной самим сервисом (манифест): предметные данные, ` +
	`которых нет в телеметрии — зависшие записи очереди, заявки в статусе, детали заказа. ` +
	`Список ручек и их параметры — в diagnostic_endpoints карточки get_service_info; вызвать можно только объявленный id ` +
	`с объявленными параметрами, только чтение. Не подходит для метрик, логов и состояния подов. ` +
	`Ручка с audience: human — ответ только для человека: его получает агент pulse и пересылает человеку как есть, ` +
	`модели он не показывается; другим клиентам такие ручки не вызываются.`

func (h *Handler) CallServiceEndpoint(ctx context.Context, request *mcp.CallToolRequest, req dto.CallServiceEndpointReq) (*mcp.CallToolResult, dto.CallServiceEndpointRep, error) {
	result, err := h.endpoints.Call(ctx, &usecaseEndpointsModel.CallReq{
		Service: req.Service, EndpointId: req.EndpointId, Params: req.Params, Human: humanClient(request),
	})
	if err != nil {
		return nil, dto.CallServiceEndpointRep{}, toolError(err)
	}
	return nil, dto.EncodeCallServiceEndpointRep(result), nil
}

// humanClient — клиенту можно получать ответы ручек для человека (внутренний токен, агент pulse).
func humanClient(request *mcp.CallToolRequest) bool {
	return request != nil && request.Extra != nil && request.Extra.TokenInfo != nil &&
		slices.Contains(request.Extra.TokenInfo.Scopes, constant.ScopeHuman)
}
