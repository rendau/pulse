package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/samber/lo"

	"github.com/mechta-market/pulse/internal/config"
	"github.com/mechta-market/pulse/internal/constant"
	domainClusterServiceP "github.com/mechta-market/pulse/internal/domain/cluster/service"
	domainDependencyRepoDbP "github.com/mechta-market/pulse/internal/domain/dependency/repo/db"
	domainDependencyServiceP "github.com/mechta-market/pulse/internal/domain/dependency/service"
	domainDeployRepoDbP "github.com/mechta-market/pulse/internal/domain/deploy/repo/db"
	domainDeployServiceP "github.com/mechta-market/pulse/internal/domain/deploy/service"
	domainEventServiceP "github.com/mechta-market/pulse/internal/domain/event/service"
	domainLogsServiceP "github.com/mechta-market/pulse/internal/domain/logs/service"
	domainSnapshotModel "github.com/mechta-market/pulse/internal/domain/snapshot/model"
	domainSnapshotServiceP "github.com/mechta-market/pulse/internal/domain/snapshot/service"
	domainSvcRepoDbP "github.com/mechta-market/pulse/internal/domain/svc/repo/db"
	domainSvcServiceP "github.com/mechta-market/pulse/internal/domain/svc/service"
	domainWorkloadRepoDbP "github.com/mechta-market/pulse/internal/domain/workload/repo/db"
	domainWorkloadServiceP "github.com/mechta-market/pulse/internal/domain/workload/service"
	handlerMcpP "github.com/mechta-market/pulse/internal/handler/mcp"
	serviceAlertmanagerServiceP "github.com/mechta-market/pulse/internal/service/alertmanager/service"
	serviceGithubServiceP "github.com/mechta-market/pulse/internal/service/github/service"
	serviceIndexerModel "github.com/mechta-market/pulse/internal/service/indexer/model"
	serviceIndexerServiceP "github.com/mechta-market/pulse/internal/service/indexer/service"
	serviceK8sServiceP "github.com/mechta-market/pulse/internal/service/k8s/service"
	serviceKusecServiceP "github.com/mechta-market/pulse/internal/service/kusec/service"
	serviceLokiServiceP "github.com/mechta-market/pulse/internal/service/loki/service"
	servicePrometheusServiceP "github.com/mechta-market/pulse/internal/service/prometheus/service"
	serviceRegistryServiceP "github.com/mechta-market/pulse/internal/service/registry/service"
	serviceRutoServiceP "github.com/mechta-market/pulse/internal/service/ruto/service"
	serviceSvcproxyModel "github.com/mechta-market/pulse/internal/service/svcproxy/model"
	serviceSvcproxyServiceP "github.com/mechta-market/pulse/internal/service/svcproxy/service"
	usecaseCatalogP "github.com/mechta-market/pulse/internal/usecase/catalog"
	usecaseClusterP "github.com/mechta-market/pulse/internal/usecase/cluster"
	usecaseDependenciesP "github.com/mechta-market/pulse/internal/usecase/dependencies"
	usecaseEndpointsP "github.com/mechta-market/pulse/internal/usecase/endpoints"
	usecaseLogsP "github.com/mechta-market/pulse/internal/usecase/logs"
	usecasePublicapiP "github.com/mechta-market/pulse/internal/usecase/publicapi"
	usecaseSnapshotP "github.com/mechta-market/pulse/internal/usecase/snapshot"
	usecaseSystemP "github.com/mechta-market/pulse/internal/usecase/system"
	usecaseTimelineP "github.com/mechta-market/pulse/internal/usecase/timeline"
)

type App struct {
	pgpool *pgxpool.Pool

	indexer *serviceIndexerServiceP.Service

	mcpServer        *mcp.Server
	httpServer       *http.Server
	systemHttpServer *http.Server

	ctx       context.Context
	ctxCancel context.CancelFunc

	exitCode int
}

func (a *App) Init() {
	var err error

	a.ctx, a.ctxCancel = context.WithCancel(context.Background())

	// logger
	initLogger(config.Conf.Debug, config.Conf.LogLevel)
	slog.Info("starting " + constant.ServiceName + " " + constant.Version)

	// rules (yaml)
	rules, err := config.LoadRules(config.Conf.RulesPath)
	errCheck(err, "config.LoadRules")

	// pgpool
	a.pgpool, err = initPgPool(config.Conf.PgDsn)
	errCheck(err, "pgpool init")

	// migrations
	{
		runMigrations()
		slog.Info("PG-migrations have been successfully applied")
	}

	// sources
	k8sService := serviceK8sServiceP.New(config.Conf.KubeConfig, config.Conf.KubeContext, rules.Indexer.ExcludeNamespaces)
	githubService := serviceGithubServiceP.New(config.Conf.GithubToken)
	registryService := serviceRegistryServiceP.New(map[string]string{"ghcr.io": config.Conf.RegistryToken})

	// опциональные источники: nil-указатель = не сконфигурирован (виден в ping как disabled)
	var prometheusService *servicePrometheusServiceP.Service
	if config.Conf.PrometheusUrl != "" {
		prometheusService = servicePrometheusServiceP.New(config.Conf.PrometheusUrl,
			servicePrometheusServiceP.Auth{Token: config.Conf.PrometheusToken, OrgId: config.Conf.PrometheusOrgId})
	}

	var lokiService *serviceLokiServiceP.Service
	if config.Conf.LokiUrl != "" {
		lokiService = serviceLokiServiceP.New(config.Conf.LokiUrl,
			serviceLokiServiceP.Auth{Token: config.Conf.LokiToken, OrgId: config.Conf.LokiOrgId})
	}

	var alertmanagerService *serviceAlertmanagerServiceP.Service
	if config.Conf.AlertmanagerUrl != "" {
		alertmanagerService = serviceAlertmanagerServiceP.New(config.Conf.AlertmanagerUrl,
			serviceAlertmanagerServiceP.Auth{Token: config.Conf.AlertmanagerToken})
	}

	var rutoService *serviceRutoServiceP.Service
	if config.Conf.RutoUrl != "" {
		rutoService = serviceRutoServiceP.New(config.Conf.RutoUrl)
	}

	var kusecService *serviceKusecServiceP.Service
	if config.Conf.KusecUrl != "" {
		kusecService = serviceKusecServiceP.New(config.Conf.KusecUrl, serviceKusecServiceP.Auth{Token: config.Conf.KusecToken})
	}

	// system
	var systemUsecase *usecaseSystemP.Usecase
	{
		sources := []usecaseSystemP.Source{
			{Name: constant.SourcePostgres, Ping: a.pgpool.Ping},
			{Name: constant.SourceK8s, Ping: k8sService.Ping},
			{Name: constant.SourceGithub, Ping: githubService.Ping},
		}
		for _, m := range lo.UniqBy(rules.ImageMapping, func(m config.ImageMapping) string { return m.Registry }) {
			sources = append(sources, usecaseSystemP.Source{
				Name: constant.SourceRegistry + ":" + m.Registry,
				Ping: func(ctx context.Context) error { return registryService.Ping(ctx, m.Registry) },
			})
		}
		sources = append(sources,
			usecaseSystemP.Source{Name: constant.SourcePrometheus, Ping: optionalPing(prometheusService)},
			usecaseSystemP.Source{Name: constant.SourceLoki, Ping: optionalPing(lokiService)},
			usecaseSystemP.Source{Name: constant.SourceAlertmanager, Ping: optionalPing(alertmanagerService)},
			usecaseSystemP.Source{Name: constant.SourceRuto, Ping: optionalPing(rutoService)},
			usecaseSystemP.Source{Name: constant.SourceKusec, Ping: optionalPing(kusecService)},
		)

		systemUsecase = usecaseSystemP.New(sources)
	}

	// svc (каталог сервисов)
	svcRepo := domainSvcRepoDbP.New(a.pgpool)
	svcService := domainSvcServiceP.New(svcRepo)

	// workload
	workloadRepo := domainWorkloadRepoDbP.New(a.pgpool)
	workloadService := domainWorkloadServiceP.New(workloadRepo)

	// deploy (история деплоев)
	deployRepo := domainDeployRepoDbP.New(a.pgpool)
	deployService := domainDeployServiceP.New(deployRepo)

	// dependency (граф связей)
	dependencyRepo := domainDependencyRepoDbP.New(a.pgpool)
	dependencyService := domainDependencyServiceP.New(dependencyRepo)

	// event (нормализация событий)
	eventService := domainEventServiceP.New()

	// indexer
	if config.Conf.IndexerEnabled {
		// nil-указатель нельзя класть в интерфейс напрямую: получится ненулевой интерфейс
		var rutoClient serviceIndexerServiceP.RutoI
		if rutoService != nil {
			rutoClient = rutoService
		}

		a.indexer = serviceIndexerServiceP.New(
			serviceIndexerModel.Config{
				Cluster:    config.Conf.ClusterName,
				Interval:   config.Conf.IndexerInterval,
				StaleAfter: rules.Indexer.StaleAfter,
				ImageMapping: lo.Map(rules.ImageMapping, func(m config.ImageMapping, _ int) serviceIndexerModel.ImageMapping {
					return serviceIndexerModel.ImageMapping{Registry: m.Registry, RepoTemplate: m.RepoTemplate, Org: m.Org, Path: m.Path}
				}),
				RutoGatewayService: rules.Ruto.GatewayService,
			},
			k8sService, githubService, registryService, svcService, workloadService, deployService, dependencyService, rutoClient,
		)
	}

	// catalog
	catalogUsecase := usecaseCatalogP.New(svcService, workloadService, k8sService)

	// logs
	var logsUsecase *usecaseLogsP.Usecase
	{
		// nil-указатель нельзя класть в интерфейс напрямую: получится ненулевой интерфейс
		var lokiClient usecaseLogsP.LokiI
		if lokiService != nil {
			lokiClient = lokiService
		}

		logsUsecase = usecaseLogsP.New(
			usecaseLogsP.Config{
				MaxLines:        rules.Logs.MaxLines,
				MaxPatterns:     rules.Logs.MaxPatterns,
				RawLimit:        rules.Logs.RawLimit,
				MaxWindow:       rules.Logs.MaxWindow,
				DefaultSelector: rules.Logs.DefaultSelector,
				ClusterSelector: rules.Logs.ClusterSelector,
				Retention:       rules.Logs.Retention,
				SearchBudget:    rules.Logs.SearchBudget,
			},
			svcService, workloadService, k8sService, lokiClient, domainLogsServiceP.New(),
		)
	}

	// snapshot
	var snapshotUsecase *usecaseSnapshotP.Usecase
	{
		rulesService := domainSnapshotServiceP.New(domainSnapshotServiceP.Config{
			AnomalyThresholdPct: rules.Snapshot.AnomalyThresholdPct,
		})

		// nil-указатель нельзя класть в интерфейс напрямую: получится ненулевой интерфейс
		var prometheusClient usecaseSnapshotP.PrometheusI
		if prometheusService != nil {
			prometheusClient = prometheusService
		}
		var alertmanagerClient usecaseSnapshotP.AlertmanagerI
		if alertmanagerService != nil {
			alertmanagerClient = alertmanagerService
		}
		// логи есть и без Loki: запасной источник — Kubernetes API (живые поды)
		var logsClient usecaseSnapshotP.LogsI = logsUsecase

		snapshotUsecase = usecaseSnapshotP.New(
			usecaseSnapshotP.Config{
				Deadline:  rules.Snapshot.Deadline,
				MaxEvents: rules.Snapshot.MaxEvents,
				MaxAlerts: rules.Snapshot.MaxAlerts,
				DefaultMetrics: lo.Map(rules.Snapshot.DefaultMetrics, func(m config.MetricDef, _ int) domainSnapshotModel.MetricDef {
					return domainSnapshotModel.MetricDef{Id: m.Id, Title: m.Title, PromQL: m.PromQL, Unit: m.Unit, Direction: m.Direction}
				}),
				PublicMetrics: lo.Map(rules.Snapshot.PublicMetrics, func(m config.MetricDef, _ int) domainSnapshotModel.MetricDef {
					return domainSnapshotModel.MetricDef{Id: m.Id, Title: m.Title, PromQL: m.PromQL, Unit: m.Unit, Direction: m.Direction}
				}),
				MaxWindow: rules.Metrics.MaxWindow,
				MaxSeries: rules.Metrics.MaxSeries,
				MaxPoints: rules.Metrics.MaxPoints,
				TopErrors: rules.Logs.TopErrors,
			},
			svcService, workloadService, dependencyService, k8sService, prometheusClient, alertmanagerClient, logsClient, eventService, rulesService,
		)
	}

	// timeline
	var timelineUsecase *usecaseTimelineP.Usecase
	{
		var kusecClient usecaseTimelineP.KusecI
		if kusecService != nil {
			kusecClient = kusecService
		}
		var prometheusClient usecaseTimelineP.PrometheusI
		if prometheusService != nil {
			prometheusClient = prometheusService
		}

		timelineUsecase = usecaseTimelineP.New(
			usecaseTimelineP.Config{
				Deadline:              rules.Timeline.Deadline,
				MaxEvents:             rules.Timeline.MaxEvents,
				CommitsLimit:          rules.Timeline.CommitsLimit,
				MaxServicesForCommits: rules.Timeline.MaxServicesForCommits,
			},
			svcService, workloadService, deployService, k8sService, githubService, kusecClient, prometheusClient,
			eventService, domainSnapshotServiceP.New(domainSnapshotServiceP.Config{AnomalyThresholdPct: rules.Snapshot.AnomalyThresholdPct}),
		)
	}

	// dependencies
	dependenciesUsecase := usecaseDependenciesP.New(
		usecaseDependenciesP.Config{MaxNodes: rules.Dependencies.MaxNodes, MaxDepth: rules.Dependencies.MaxDepth, HealthDeadline: rules.Snapshot.Deadline},
		svcService, workloadService, dependencyService, k8sService,
		domainSnapshotServiceP.New(domainSnapshotServiceP.Config{AnomalyThresholdPct: rules.Snapshot.AnomalyThresholdPct}),
	)

	// public api (внешний контур через gateway ruto)
	var publicApiUsecase *usecasePublicapiP.Usecase
	{
		var rutoClient usecasePublicapiP.RutoI
		if rutoService != nil {
			rutoClient = rutoService
		}
		var prometheusClient usecasePublicapiP.PrometheusI
		if prometheusService != nil {
			prometheusClient = prometheusService
		}

		publicApiUsecase = usecasePublicapiP.New(
			usecasePublicapiP.Config{
				Deadline:       rules.Ruto.Deadline,
				MaxEndpoints:   rules.Ruto.MaxEndpoints,
				RequestsMetric: rules.Ruto.RequestsMetric,
				DurationMetric: rules.Ruto.DurationMetric,
			},
			svcService, dependencyService, rutoClient, prometheusClient,
		)
	}

	// endpoints (прокси к диагностическим ручкам)
	var endpointsUsecase *usecaseEndpointsP.Usecase
	{
		var caller usecaseEndpointsP.CallerI = serviceSvcproxyServiceP.New(config.Conf.ClusterDomain)
		if config.Conf.EndpointCallMode == "k8s-proxy" {
			caller = k8sProxyCaller{k8sService}
			slog.Info("endpoint calls go through kubernetes API proxy (services/proxy)")
		}

		endpointsUsecase = usecaseEndpointsP.New(
			usecaseEndpointsP.Config{
				MaxRows:      rules.Endpoints.MaxRows,
				MaxBodyBytes: rules.Endpoints.MaxBodyBytes,
				MaxTimeout:   rules.Endpoints.MaxTimeout,
				DefaultPort:  rules.Endpoints.DefaultPort,
			},
			svcService, workloadService, caller,
		)
	}

	// cluster (здоровье кластера)
	var clusterUsecase *usecaseClusterP.Usecase
	{
		var prometheusClient usecaseClusterP.PrometheusI
		if prometheusService != nil {
			prometheusClient = prometheusService
		}
		var alertmanagerClient usecaseClusterP.AlertmanagerI
		if alertmanagerService != nil {
			alertmanagerClient = alertmanagerService
		}

		clusterUsecase = usecaseClusterP.New(
			usecaseClusterP.Config{
				Deadline:        rules.Cluster.Deadline,
				MaxProblemPods:  rules.Cluster.MaxProblemPods,
				MaxEventReasons: rules.Cluster.MaxEventReasons,
				MaxInfraAlerts:  rules.Cluster.MaxInfraAlerts,
				MaxLogServices:  rules.Cluster.MaxLogServices,
				Metrics: lo.Map(rules.Cluster.Metrics, func(m config.MetricDef, _ int) domainSnapshotModel.MetricDef {
					return domainSnapshotModel.MetricDef{Id: m.Id, Title: m.Title, PromQL: m.PromQL, Unit: m.Unit, Direction: m.Direction}
				}),
			},
			workloadService, k8sService, prometheusClient, alertmanagerClient, logsUsecase,
			domainClusterServiceP.New(domainClusterServiceP.Config{
				PendingPodsThreshold: rules.Cluster.PendingPodsThreshold,
				ProblemPodsThreshold: rules.Cluster.ProblemPodsThreshold,
			}),
			domainSnapshotServiceP.New(domainSnapshotServiceP.Config{AnomalyThresholdPct: rules.Snapshot.AnomalyThresholdPct}),
		)
	}

	// mcp server
	{
		handler := handlerMcpP.New(systemUsecase, catalogUsecase, snapshotUsecase, logsUsecase, timelineUsecase, dependenciesUsecase, publicApiUsecase, endpointsUsecase, clusterUsecase)
		a.mcpServer = MCPServerCreate(handler.Register)
		a.httpServer = MCPHttpServerCreate(config.Conf.HttpPort, config.Conf.MCPPath,
			append([]string{config.Conf.MCPAuthToken}, config.Conf.MCPExternalTokens...), a.mcpServer)
	}

	// system http server (healthcheck, readiness, docs, metrics)
	{
		a.systemHttpServer = SystemHttpServerCreate(config.Conf.SystemHttpPort, a.pgpool.Ping)
	}
}

func (a *App) PreStartHook() {
	slog.Info("PreStartHook")
}

func (a *App) Start() {
	slog.Info("Starting")

	// indexer
	if a.indexer != nil {
		a.indexer.Start(a.ctx)
		slog.Info("indexer started", "interval", config.Conf.IndexerInterval.String())
	}

	// mcp http server
	{
		go func() {
			err := a.httpServer.ListenAndServe()
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCheck(err, "http-server stopped")
			}
		}()
		slog.Info("http-server started " + a.httpServer.Addr + config.Conf.MCPPath)
	}

	// system http server
	{
		go func() {
			err := a.systemHttpServer.ListenAndServe()
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCheck(err, "system-http-server stopped")
			}
		}()
		slog.Info("system-http-server started " + a.systemHttpServer.Addr)
	}
}

func (a *App) Listen() {
	signalCtx, signalCtxCancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer signalCtxCancel()

	// wait signal
	<-signalCtx.Done()
}

func (a *App) Stop() {
	slog.Info("Shutting down...")

	// stop context
	a.ctxCancel()

	// mcp http server
	{
		ctx, ctxCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer ctxCancel()

		if err := a.httpServer.Shutdown(ctx); err != nil {
			slog.Error("http-server shutdown error", "error", err)
			a.exitCode = 1
		}
	}

	// system http server
	{
		ctx, ctxCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer ctxCancel()

		if err := a.systemHttpServer.Shutdown(ctx); err != nil {
			slog.Error("system-http-server shutdown error", "error", err)
			a.exitCode = 1
		}
	}
}

func (a *App) WaitJobs() {
	slog.Info("waiting jobs")

	// indexer
	if a.indexer != nil {
		a.indexer.Wait()
	}
}

func (a *App) Exit() {
	slog.Info("Exit")

	a.pgpool.Close()

	os.Exit(a.exitCode)
}

// k8sProxyCaller адаптирует ProxyGet клиента k8s под порт вызова ручек (локальная разработка).
type k8sProxyCaller struct {
	k8s *serviceK8sServiceP.Service
}

func (c k8sProxyCaller) Get(ctx context.Context, namespace, service string, port int, path string, query map[string]string, maxBytes int64) (*serviceSvcproxyModel.Response, error) {
	body, err := c.k8s.ProxyGet(ctx, namespace, service, port, path, query)
	if err != nil {
		return nil, err
	}
	resp := &serviceSvcproxyModel.Response{StatusCode: http.StatusOK, Body: body}
	if int64(len(body)) > maxBytes {
		resp.Body, resp.Truncated = body[:maxBytes], true
	}
	return resp, nil
}

// pinger — общий срез клиентов источников для ping.
type pinger interface {
	Ping(ctx context.Context) error
}

// optionalPing возвращает nil для выключенного конфигом источника (nil-указатель),
// чтобы ping показал его как disabled, а не как ошибку.
func optionalPing[T any, PT interface {
	*T
	pinger
}](client PT) func(ctx context.Context) error {
	if client == nil {
		return nil
	}
	return client.Ping
}

func errCheck(err error, msg string) {
	if err != nil {
		if msg != "" {
			err = fmt.Errorf("%s: %w", msg, err)
		}
		slog.Error(err.Error())
		os.Exit(1)
	}
}
