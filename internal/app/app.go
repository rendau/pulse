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
	serviceLokiServiceP "github.com/mechta-market/pulse/internal/service/loki/service"
	servicePrometheusServiceP "github.com/mechta-market/pulse/internal/service/prometheus/service"
	serviceRegistryServiceP "github.com/mechta-market/pulse/internal/service/registry/service"
	usecaseCatalogP "github.com/mechta-market/pulse/internal/usecase/catalog"
	usecaseSystemP "github.com/mechta-market/pulse/internal/usecase/system"
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
	registryService := serviceRegistryServiceP.New(map[string]string{"ghcr.io": config.Conf.GithubToken})

	var prometheusService *servicePrometheusServiceP.Service
	if config.Conf.PrometheusUrl != "" {
		prometheusService = servicePrometheusServiceP.New(config.Conf.PrometheusUrl)
	}

	var lokiService *serviceLokiServiceP.Service
	if config.Conf.LokiUrl != "" {
		lokiService = serviceLokiServiceP.New(config.Conf.LokiUrl)
	}

	var alertmanagerService *serviceAlertmanagerServiceP.Service
	if config.Conf.AlertmanagerUrl != "" {
		alertmanagerService = serviceAlertmanagerServiceP.New(config.Conf.AlertmanagerUrl)
	}

	// system
	var systemUsecase *usecaseSystemP.Usecase
	{
		sources := []usecaseSystemP.Source{
			{Name: constant.SourcePostgres, Ping: a.pgpool.Ping},
			{Name: constant.SourceK8s, Ping: k8sService.Ping},
			{Name: constant.SourceGithub, Ping: githubService.Ping},
		}
		for _, m := range rules.ImageMapping {
			sources = append(sources, usecaseSystemP.Source{
				Name: constant.SourceRegistry + ":" + m.Registry,
				Ping: func(ctx context.Context) error { return registryService.Ping(ctx, m.Registry) },
			})
		}
		sources = append(sources,
			usecaseSystemP.Source{Name: constant.SourcePrometheus, Ping: optionalPing(prometheusService)},
			usecaseSystemP.Source{Name: constant.SourceLoki, Ping: optionalPing(lokiService)},
			usecaseSystemP.Source{Name: constant.SourceAlertmanager, Ping: optionalPing(alertmanagerService)},
		)

		systemUsecase = usecaseSystemP.New(sources)
	}

	// svc (каталог сервисов)
	svcRepo := domainSvcRepoDbP.New(a.pgpool)
	svcService := domainSvcServiceP.New(svcRepo)

	// workload
	workloadRepo := domainWorkloadRepoDbP.New(a.pgpool)
	workloadService := domainWorkloadServiceP.New(workloadRepo)

	// indexer
	if config.Conf.IndexerEnabled {
		a.indexer = serviceIndexerServiceP.New(
			serviceIndexerModel.Config{
				Cluster:    config.Conf.ClusterName,
				Interval:   config.Conf.IndexerInterval,
				StaleAfter: rules.Indexer.StaleAfter,
				ImageMapping: lo.Map(rules.ImageMapping, func(m config.ImageMapping, _ int) serviceIndexerModel.ImageMapping {
					return serviceIndexerModel.ImageMapping{Registry: m.Registry, RepoTemplate: m.RepoTemplate, Org: m.Org}
				}),
			},
			k8sService, githubService, registryService, svcService, workloadService,
		)
	}

	// catalog
	catalogUsecase := usecaseCatalogP.New(svcService, workloadService, k8sService)

	// mcp server
	{
		handler := handlerMcpP.New(systemUsecase, catalogUsecase)
		a.mcpServer = MCPServerCreate(handler.Register)
		a.httpServer = MCPHttpServerCreate(config.Conf.HttpPort, config.Conf.MCPPath, config.Conf.MCPAuthToken, a.mcpServer)
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
