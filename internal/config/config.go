package config

import (
	"time"

	"github.com/caarlos0/env/v9"
	_ "github.com/joho/godotenv/autoload"
)

// Conf — параметры окружения: подключения, порты, токены.
// Структурные правила (маппинг образов, исключения namespace) — в yaml, см. rules.go.
var Conf = struct {
	Debug    bool   `env:"DEBUG" envDefault:"false"`
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`

	HttpPort       string `env:"HTTP_PORT" envDefault:"80"`
	SystemHttpPort string `env:"SYSTEM_HTTP_PORT" envDefault:"3003"` // healthcheck, readiness, metrics, docs

	// MCP
	MCPAuthToken string `env:"MCP_AUTH_TOKEN"` // пусто — без проверки (локальная разработка)
	MCPPath      string `env:"MCP_PATH" envDefault:"/mcp"`

	// путь к yaml с правилами; отсутствие файла — не ошибка, берутся дефолты
	RulesPath string `env:"RULES_PATH" envDefault:"./conf.yml"`

	PgDsn string `env:"PG_DSN"`

	// kubernetes: пусто — in-cluster конфиг; иначе путь к kubeconfig (локальная разработка)
	KubeConfig  string `env:"KUBECONFIG"`
	KubeContext string `env:"KUBE_CONTEXT"`
	ClusterName string `env:"CLUSTER_NAME" envDefault:"default"`

	// github; RegistryToken — доступ к ghcr.io (по умолчанию тот же GITHUB_TOKEN)
	GithubToken   string `env:"GITHUB_TOKEN"`
	RegistryToken string `env:"REGISTRY_TOKEN" envDefault:"${GITHUB_TOKEN}" envExpand:"true"`

	// источники телеметрии: пустой URL — источник выключен.
	// Авторизация: *_TOKEN — bearer; basic-auth — через userinfo в URL (https://user:pass@host);
	// *_ORG_ID — заголовок X-Scope-OrgID для мультитенантных Loki/Mimir/Cortex.
	PrometheusUrl   string `env:"PROMETHEUS_URL"`
	PrometheusToken string `env:"PROMETHEUS_TOKEN"`
	PrometheusOrgId string `env:"PROMETHEUS_ORG_ID"`

	LokiUrl   string `env:"LOKI_URL"`
	LokiToken string `env:"LOKI_TOKEN"`
	LokiOrgId string `env:"LOKI_ORG_ID"`

	AlertmanagerUrl   string `env:"ALERTMANAGER_URL"`
	AlertmanagerToken string `env:"ALERTMANAGER_TOKEN"`

	// kusec: configmaps, secrets, env (интеграция после получения проекта kusec)
	KusecUrl   string `env:"KUSEC_URL"`
	KusecToken string `env:"KUSEC_TOKEN"`

	// диагностические ручки сервисов (фаза 6): direct — на ClusterIP через DNS name.namespace.svc
	// (в кластере); k8s-proxy — через API-сервер (локальная разработка, нужен RBAC services/proxy)
	EndpointCallMode string `env:"ENDPOINT_CALL_MODE" envDefault:"direct"`
	ClusterDomain    string `env:"CLUSTER_DOMAIN" envDefault:"svc"`

	// индексер топологии
	IndexerInterval time.Duration `env:"INDEXER_INTERVAL" envDefault:"5m"`
	IndexerEnabled  bool          `env:"INDEXER_ENABLED" envDefault:"true"`
}{}

func init() {
	if err := env.Parse(&Conf); err != nil {
		panic(err)
	}
}
