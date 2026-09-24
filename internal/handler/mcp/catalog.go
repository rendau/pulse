package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mechta-market/pulse/internal/handler/mcp/dto"
)

const (
	resolveServiceDescription = `Переводит человеческую формулировку («платежи», «kafka producer», «сайт») в имена сервисов каталога. ` +
		`Вызывай ПЕРВЫМ, когда пользователь назвал систему не точным именем; не нужен, если имя уже получено от этого инструмента или list_services. ` +
		`Понимает кириллицу (транслитерация: «караван» → caravan) и имена workload'ов (notifire-sms → sms). ` +
		`Возвращает до 5 кандидатов с уверенностью; при ambiguous=true переспроси пользователя.`

	listServicesDescription = `Обзорный список сервисов каталога с фильтрами по команде, namespace, критичности и наличию service.yaml. ` +
		`Выбирай для вопросов «какие сервисы у команды X», «что крутится в namespace Y», «сколько всего сервисов». ` +
		`Не подходит для поиска одного сервиса по описанию — для этого resolve_service. Возвращает компактные строки: имя, title, команда, namespaces.`

	getServiceInfoDescription = `Карточка сервиса: владелец, критичность, алиасы, репозиторий, раннбуки, объявленные метрики, ` +
		`workloads в кластере с образом, задеплоенным коммитом и живым состоянием подов (ready/total, рестарты, проблемы). ` +
		`Выбирай для «что это за сервис», «кто владелец», «какая версия в проде», «сколько подов живо». Требует точное имя сервиса.`
)

func (h *Handler) ResolveService(ctx context.Context, _ *mcp.CallToolRequest, req dto.ResolveServiceReq) (*mcp.CallToolResult, dto.ResolveServiceRep, error) {
	result, err := h.catalog.Resolve(ctx, req.Query)
	if err != nil {
		return nil, dto.ResolveServiceRep{}, toolError(err)
	}
	return nil, dto.EncodeResolveServiceRep(result), nil
}

func (h *Handler) ListServices(ctx context.Context, _ *mcp.CallToolRequest, req dto.ListServicesReq) (*mcp.CallToolResult, dto.ListServicesRep, error) {
	pars := dto.DecodeListServicesReq(req)

	items, total, err := h.catalog.List(ctx, pars)
	if err != nil {
		return nil, dto.ListServicesRep{}, toolError(err)
	}
	return nil, dto.EncodeListServicesRep(items, total, pars.Page, pars.PageSize), nil
}

func (h *Handler) GetServiceInfo(ctx context.Context, _ *mcp.CallToolRequest, req dto.GetServiceInfoReq) (*mcp.CallToolResult, dto.ServiceInfoRep, error) {
	result, err := h.catalog.Info(ctx, req.Service)
	if err != nil {
		return nil, dto.ServiceInfoRep{}, toolError(err)
	}
	return nil, dto.EncodeServiceInfoRep(result), nil
}
