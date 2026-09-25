package app

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/mechta-market/pulse/internal/constant"
	"github.com/mechta-market/pulse/internal/infra/pulsekit"
	serviceIndexerServiceP "github.com/mechta-market/pulse/internal/service/indexer/service"
)

// манифест самого pulse (docs/service-manifest.md): pulse — первый сервис по своему стандарту

func newPulsekit() *pulsekit.Kit {
	return pulsekit.New(pulsekit.Config{}, pulsekit.Service{
		Name:  constant.ServiceName,
		Title: "pulse — инфраструктурный контекст для агентов",
		Description: "MCP-сервер: каталог сервисов, их состояние в Kubernetes, метрики, логи, изменения и манифесты " +
			"сервисов — для LLM-агентов, Telegram-бота и дежурных. Только чтение.",
		Aliases:     []string{"пульс", "pulse-mcp", "infra-mcp"},
		OwnerTeam:   "platform",
		Criticality: "low",
		RepoUrl:     "https://github.com/mechta-market/pulse",
	}, pulsekit.Build{Version: constant.Version, Commit: constant.Commit, BuiltAt: constant.BuiltAt})
}

// hostOf — хост адреса источника для target зависимости: без схемы, пути и учётных данных.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host
}

// indexerCycleRep — ответ диагностической ручки indexer_last_cycle.
type indexerCycleRep struct {
	FinishedAt        time.Time `json:"finished_at"`
	DurationMs        int64     `json:"duration_ms"`
	Workloads         int       `json:"workloads"`
	Services          int       `json:"services"`
	WithMetadata      int       `json:"with_metadata"`
	WithManifest      int       `json:"with_manifest"`
	MetadataErrors    int       `json:"metadata_errors"`
	CommitsResolved   int       `json:"commits_resolved"`
	GithubUnavailable bool      `json:"github_unavailable"`
	Error             string    `json:"error,omitempty"`
}

func handleIndexerCycle(kit *pulsekit.Kit, indexer *serviceIndexerServiceP.Service) {
	pulsekit.Handle(kit, pulsekit.Endpoint{
		Id:    "indexer_last_cycle",
		Title: "Последний цикл индексера",
		Description: "Вызывай, когда каталог выглядит устаревшим или неполным (нет сервиса, старый коммит, " +
			"не видно манифеста): когда индексер последний раз обошёл кластер, сколько нашёл, были ли ошибки GitHub.",
		Path:    "/diag/indexer",
		Timeout: time.Second,
	}, func(context.Context, map[string]string) (indexerCycleRep, error) {
		if indexer == nil {
			return indexerCycleRep{}, pulsekit.Error{Status: http.StatusNotFound, Message: "индексер выключен (INDEXER_ENABLED=false)"}
		}
		c := indexer.LastCycle()
		if c == nil {
			return indexerCycleRep{}, pulsekit.Error{Status: http.StatusNotFound, Message: "индексер ещё не завершил ни одного цикла"}
		}
		return indexerCycleRep{
			FinishedAt: c.FinishedAt, DurationMs: c.Duration.Milliseconds(), Workloads: c.Workloads, Services: c.Services,
			WithMetadata: c.WithMetadata, WithManifest: c.WithManifest, MetadataErrors: c.MetadataErrors,
			CommitsResolved: c.CommitsResolved, GithubUnavailable: c.GithubUnavailable, Error: c.Error,
		}, nil
	})
}
