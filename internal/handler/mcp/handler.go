// Package mcp — транспортный слой MCP: регистрация инструментов и маппинг
// usecase-моделей в JSON-ответы через dto.
package mcp

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mechta-market/pulse/internal/usecase/catalog"
	"github.com/mechta-market/pulse/internal/usecase/cluster"
	"github.com/mechta-market/pulse/internal/usecase/dependencies"
	"github.com/mechta-market/pulse/internal/usecase/endpoints"
	"github.com/mechta-market/pulse/internal/usecase/logs"
	"github.com/mechta-market/pulse/internal/usecase/publicapi"
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
	publicApi    publicapi.PublicApiI
	endpoints    endpoints.EndpointsI
	cluster      cluster.ClusterI
}

func New(
	system system.SystemI,
	catalog catalog.CatalogI,
	snapshot snapshot.SnapshotI,
	logs logs.LogsI,
	timeline timeline.TimelineI,
	dependencies dependencies.DependenciesI,
	publicApi publicapi.PublicApiI,
	endpoints endpoints.EndpointsI,
	cluster cluster.ClusterI,
) *Handler {
	return &Handler{
		system: system, catalog: catalog, snapshot: snapshot, logs: logs,
		timeline: timeline, dependencies: dependencies, publicApi: publicApi, endpoints: endpoints, cluster: cluster,
	}
}

// Register добавляет все инструменты на сервер. Описания инструментов — часть продукта:
// они конкурируют за контекст модели, поэтому коротко и с указанием когда выбирать.
func (h *Handler) Register(server *mcp.Server) {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(false)}

	addTool(server, &mcp.Tool{
		Name:        "ping",
		Description: pingDescription,
		Annotations: readOnly,
	}, h.Ping)

	addTool(server, &mcp.Tool{
		Name:        "resolve_service",
		Description: resolveServiceDescription,
		Annotations: readOnly,
	}, h.ResolveService)

	addTool(server, &mcp.Tool{
		Name:        "list_services",
		Description: listServicesDescription,
		Annotations: readOnly,
	}, h.ListServices)

	addTool(server, &mcp.Tool{
		Name:        "get_service_info",
		Description: getServiceInfoDescription,
		Annotations: readOnly,
	}, h.GetServiceInfo)

	addTool(server, &mcp.Tool{
		Name:        "get_service_snapshot",
		Description: getServiceSnapshotDescription,
		Annotations: readOnly,
	}, h.GetServiceSnapshot)

	addTool(server, &mcp.Tool{
		Name:        "query_metrics",
		Description: queryMetricsDescription,
		Annotations: readOnly,
	}, h.QueryMetrics)

	addTool(server, &mcp.Tool{
		Name:        "query_logs",
		Description: queryLogsDescription,
		Annotations: readOnly,
	}, h.QueryLogs)

	addTool(server, &mcp.Tool{
		Name:        "get_timeline",
		Description: getTimelineDescription,
		Annotations: readOnly,
	}, h.GetTimeline)

	addTool(server, &mcp.Tool{
		Name:        "get_changes",
		Description: getChangesDescription,
		Annotations: readOnly,
	}, h.GetChanges)

	addTool(server, &mcp.Tool{
		Name:        "get_dependencies",
		Description: getDependenciesDescription,
		Annotations: readOnly,
	}, h.GetDependencies)

	addTool(server, &mcp.Tool{
		Name:        "get_public_api",
		Description: getPublicApiDescription,
		Annotations: readOnly,
	}, h.GetPublicApi)

	addTool(server, &mcp.Tool{
		Name:        "call_service_endpoint",
		Description: callServiceEndpointDescription,
		Annotations: readOnly,
	}, h.CallServiceEndpoint)

	addTool(server, &mcp.Tool{
		Name:        "get_cluster_health",
		Description: getClusterHealthDescription,
		Annotations: readOnly,
	}, h.GetClusterHealth)
}
