// Package mcp — транспортный слой MCP: регистрация инструментов и маппинг
// usecase-моделей в JSON-ответы через dto.
package mcp

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mechta-market/pulse/internal/usecase/catalog"
	"github.com/mechta-market/pulse/internal/usecase/dependencies"
	"github.com/mechta-market/pulse/internal/usecase/endpoints"
	"github.com/mechta-market/pulse/internal/usecase/logs"
	"github.com/mechta-market/pulse/internal/usecase/snapshot"
	"github.com/mechta-market/pulse/internal/usecase/system"
	"github.com/mechta-market/pulse/internal/usecase/timeline"
)

type Handler struct {
	system       system.SystemI
	catalog      catalog.CatalogI
	snapshot     snapshot.SnapshotI
	logs         logs.LogsI
	timeline     timeline.TimelineI
	dependencies dependencies.DependenciesI
	endpoints    endpoints.EndpointsI
}

func New(
	system system.SystemI,
	catalog catalog.CatalogI,
	snapshot snapshot.SnapshotI,
	logs logs.LogsI,
	timeline timeline.TimelineI,
	dependencies dependencies.DependenciesI,
	endpoints endpoints.EndpointsI,
) *Handler {
	return &Handler{
		system: system, catalog: catalog, snapshot: snapshot, logs: logs,
		timeline: timeline, dependencies: dependencies, endpoints: endpoints,
	}
}

// Register добавляет все инструменты на сервер. Описания инструментов — часть продукта:
// они конкурируют за контекст модели, поэтому коротко и с указанием когда выбирать.
func (h *Handler) Register(server *mcp.Server) {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(false)}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ping",
		Description: pingDescription,
		Annotations: readOnly,
	}, h.Ping)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "resolve_service",
		Description: resolveServiceDescription,
		Annotations: readOnly,
	}, h.ResolveService)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_services",
		Description: listServicesDescription,
		Annotations: readOnly,
	}, h.ListServices)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_service_info",
		Description: getServiceInfoDescription,
		Annotations: readOnly,
	}, h.GetServiceInfo)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_service_snapshot",
		Description: getServiceSnapshotDescription,
		Annotations: readOnly,
	}, h.GetServiceSnapshot)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "query_metrics",
		Description: queryMetricsDescription,
		Annotations: readOnly,
	}, h.QueryMetrics)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "query_logs",
		Description: queryLogsDescription,
		Annotations: readOnly,
	}, h.QueryLogs)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_timeline",
		Description: getTimelineDescription,
		Annotations: readOnly,
	}, h.GetTimeline)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_changes",
		Description: getChangesDescription,
		Annotations: readOnly,
	}, h.GetChanges)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_dependencies",
		Description: getDependenciesDescription,
		Annotations: readOnly,
	}, h.GetDependencies)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "call_service_endpoint",
		Description: callServiceEndpointDescription,
		Annotations: readOnly,
	}, h.CallServiceEndpoint)
}
