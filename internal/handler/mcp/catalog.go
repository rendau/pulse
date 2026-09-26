package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rendau/pulse/internal/handler/mcp/dto"
)

const (
	resolveServiceDescription = `Переводит человеческую формулировку («платежи», «kafka producer», «сайт») в имена сервисов каталога. ` +
		`Вызывай ПЕРВЫМ, когда пользователь назвал систему не точным именем; не нужен, если имя уже получено от этого инструмента или list_services. ` +
		`Понимает кириллицу (транслитерация: «караван» → caravan) и имена workload'ов (notifire-sms → sms). ` +
		`Возвращает до 5 кандидатов с уверенностью; при ambiguous=true переспроси пользователя.`

	listServicesDescription = `Обзорный список сервисов каталога с фильтрами по команде, namespace, критичности и наличию метаданных. ` +
		`Выбирай для вопросов «какие сервисы у команды X», «что крутится в namespace Y», «сколько всего сервисов», «у кого есть манифест». ` +
		`Не подходит для поиска одного сервиса по описанию — для этого resolve_service. ` +
		`Возвращает компактные строки: имя, title, команда, namespaces, статус манифеста сервиса.`

	getServiceInfoDescription = `Карточка сервиса: владелец, критичность, алиасы, репозиторий, раннбуки, объявленные метрики, ` +
		`зависимости и диагностические ручки (из манифеста сервиса), бизнес-смысл (domain: за что отвечает и что нет, объекты — ` +
		`формат номера, что значат статусы и когда объект застрял, типичные вопросы), workloads с образом, задеплоенным коммитом, ` +
		`живым состоянием подов и результатом поиска манифеста. ` +
		`Выбирай для «что это за сервис», «за что отвечает», «что значит статус», «кто владелец», «какая версия в проде», «от чего зависит». Требует точное имя сервиса.`
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
