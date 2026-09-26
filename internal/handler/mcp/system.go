package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rendau/pulse/internal/handler/mcp/dto"
)

const pingDescription = `Проверка сервиса: версия и статус подключения к каждому источнику данных ` +
	`(kubernetes, github, prometheus, loki, alertmanager, postgres). ` +
	`Выбирай, когда другие инструменты возвращают ошибки источников или нужно понять, ` +
	`какие данные вообще доступны. Не подходит для вопросов о состоянии сервисов компании.`

func (h *Handler) Ping(ctx context.Context, _ *mcp.CallToolRequest, _ dto.EmptyReq) (*mcp.CallToolResult, dto.PingRep, error) {
	return nil, dto.EncodePing(h.system.Ping(ctx)), nil
}
