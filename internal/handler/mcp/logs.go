package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mechta-market/pulse/internal/errs"
	"github.com/mechta-market/pulse/internal/handler/mcp/dto"
	usecaseLogsModel "github.com/mechta-market/pulse/internal/usecase/logs/model"
	"github.com/mechta-market/pulse/internal/util/tz"
)

const queryLogsDescription = `Логи сервиса из Loki, по умолчанию агрегированные в паттерны: тысяча одинаковых ошибок ` +
	`приходит одной строкой со счётчиком, примером и временем первого/последнего появления. ` +
	`Выбирай после get_service_snapshot, чтобы узнать, НА ЧЁМ именно падает сервис (level=error), или проверить конкретную ошибку (pattern). ` +
	`mode=raw — последние строки (≤100) для деталей; workload — только один workload сервиса (у паттерна видно, из каких он). ` +
	`Без service — поиск pattern (номер заказа, id клиента; ищется целым словом) во всех логах кластера назад по суткам, ` +
	`пока не найдёт следы (до 30 дней, ≤1 мин); services — где и сколько, lines — последние строки по порядку событий. ` +
	`end — конец окна в прошлом: дата (эти сутки) или время. ` +
	`Телефон в pattern — с «+» (+77011234567): ищется в любом написании; карты в строках маскированы. Без Loki — из Kubernetes (source=kubernetes: только живые поды, хвост). ` +
	`Не подходит для метрик и событий кластера.`

func (h *Handler) QueryLogs(ctx context.Context, _ *mcp.CallToolRequest, req dto.QueryLogsReq) (*mcp.CallToolResult, dto.QueryLogsRep, error) {
	// окно по умолчанию выбирает usecase: у логов сервиса 1h, у поиска по всем сервисам —
	// назад по суткам, у дня в end — эти сутки
	win, err := parseWindowDefault(req.Window, 0)
	if err != nil {
		return nil, dto.QueryLogsRep{}, toolError(err)
	}
	var end time.Time
	var endIsDay bool
	if s := strings.TrimSpace(req.End); s != "" {
		if end, endIsDay, err = tz.ParseEnd(s); err != nil {
			return nil, dto.QueryLogsRep{}, toolError(fmt.Errorf("%w: %s", errs.InvalidRequest, err))
		}
	}

	result, err := h.logs.Query(ctx, &usecaseLogsModel.QueryReq{
		Service:  req.Service,
		Level:    req.Level,
		Pattern:  req.Pattern,
		Window:   win,
		Mode:     req.Mode,
		Limit:    req.Limit,
		Workload: req.Workload,
		End:      end,
		EndIsDay: endIsDay,
	})
	if err != nil {
		return nil, dto.QueryLogsRep{}, toolError(err)
	}
	return nil, dto.EncodeQueryLogsRep(result), nil
}
