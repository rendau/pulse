package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rendau/pulse/internal/handler/mcp/dto"
)

const getPublicApiDescription = `Внешний контур сервиса через API-gateway ruto: домен, опубликованные маршруты и трафик по ним ` +
	`за окно (rps, доля 5xx, p95, распределение кодов) — то, что видят внешние клиенты. ` +
	`Выбирай для «клиенты жалуются, а внутри всё зелёное», «какие ручки открыты наружу», «какой маршрут сыпет 502». ` +
	`Расхождение с внутренними метриками снапшота — сам по себе сигнал (проблема на gateway или в сети). ` +
	`Сервис, не опубликованный в ruto, — ошибка; внутренние ручки — get_service_info.`

func (h *Handler) GetPublicApi(ctx context.Context, _ *mcp.CallToolRequest, req dto.GetPublicApiReq) (*mcp.CallToolResult, dto.PublicApiRep, error) {
	win, err := parseWindow(req.Window)
	if err != nil {
		return nil, dto.PublicApiRep{}, toolError(err)
	}

	result, err := h.publicApi.PublicApi(ctx, req.Service, win)
	if err != nil {
		return nil, dto.PublicApiRep{}, toolError(err)
	}
	return nil, dto.EncodePublicApiRep(result), nil
}
