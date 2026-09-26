package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rendau/pulse/internal/handler/mcp/dto"
	usecaseDependenciesModel "github.com/rendau/pulse/internal/usecase/dependencies/model"
)

const getDependenciesDescription = `Граф связей сервиса: от кого зависит (upstream) и кто зависит от него (downstream), ` +
	`с адресом, портом и кратким здоровьем соседа. Связи взяты из конфигурации (env/configmap и маршруты gateway ruto) — это ` +
	`СКОНФИГУРИРОВАННЫЕ связи, а не фактический трафик: для «включена ли интеграция» точно, для «кто сейчас реально ходит» — нет. ` +
	`Выбирай для «а это точно наша проблема?», «кого ещё задело», «от чего зависят платежи».`

func (h *Handler) GetDependencies(ctx context.Context, _ *mcp.CallToolRequest, req dto.GetDependenciesReq) (*mcp.CallToolResult, dto.DependenciesRep, error) {
	result, err := h.dependencies.Graph(ctx, &usecaseDependenciesModel.GraphReq{Service: req.Service, Direction: req.Direction, Depth: req.Depth})
	if err != nil {
		return nil, dto.DependenciesRep{}, toolError(err)
	}
	return nil, dto.EncodeDependenciesRep(result), nil
}
