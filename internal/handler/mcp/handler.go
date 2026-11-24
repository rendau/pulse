// Package mcp — транспортный слой MCP: регистрация инструментов и маппинг
// usecase-моделей в JSON-ответы через dto.
package mcp

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rendau/pulse/internal/usecase/catalog"
	"github.com/rendau/pulse/internal/usecase/cluster"
	"github.com/rendau/pulse/internal/usecase/dependencies"
	"github.com/rendau/pulse/internal/usecase/edge"
	"github.com/rendau/pulse/internal/usecase/endpoints"
	"github.com/rendau/pulse/internal/usecase/finding"
	"github.com/rendau/pulse/internal/usecase/logs"
	"github.com/rendau/pulse/internal/usecase/publicapi"
	"github.com/rendau/pulse/internal/usecase/snapshot"
	"github.com/rendau/pulse/internal/usecase/system"
	"github.com/rendau/pulse/internal/usecase/timeline"
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
	edge         edge.EdgeI
	// finding — nil: находки не отправляются (нет PULSE_AGENT_KEY), инструмента нет
	finding finding.FindingI
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
	edge edge.EdgeI,
	finding finding.FindingI,
) *Handler {
	return &Handler{
		system: system, catalog: catalog, snapshot: snapshot, logs: logs,
		timeline: timeline, dependencies: dependencies, publicApi: publicApi, endpoints: endpoints, cluster: cluster,
		edge: edge, finding: finding,
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

	addTool(server, &mcp.Tool{
		Name:        "get_edge_traffic",
		Description: getEdgeTrafficDescription,
		Annotations: readOnly,
	}, h.GetEdgeTraffic)

	addTool(server, &mcp.Tool{
		Name:        "get_client_activity",
		Description: getClientActivityDescription,
		Annotations: readOnly,
	}, h.GetClientActivity)
}

// RegisterExternal добавляет инструменты только для внешних клиентов (коннекторы, внешние токены):
// агенту pulse-agent (MCP_AUTH_TOKEN) их не видно — находки у него от своего наблюдателя.
func (h *Handler) RegisterExternal(server *mcp.Server) {
	h.Register(server)

	if h.finding != nil {
		addTool(server, &mcp.Tool{
			Name:        "report_security_finding",
			Description: reportSecurityFindingDescription,
			// не только читает: запись в ленту, но ничего не удаляет и не меняет в кластере
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(false), IdempotentHint: false, OpenWorldHint: new(false)},
		}, h.ReportSecurityFinding)
	}
}
