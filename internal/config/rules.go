package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"go.yaml.in/yaml/v3"
)

// Rules — структурные правила из yaml-файла (RULES_PATH). Всё, что неудобно
// выражать плоскими env: маппинг образов на репозитории, исключения namespace.
type Rules struct {
	Indexer struct {
		// namespace'ы, которые индексер пропускает
		ExcludeNamespaces []string `yaml:"exclude_namespaces"`
		// workload, не видевшийся дольше этого срока, удаляется из каталога
		StaleAfter time.Duration `yaml:"stale_after"`
	} `yaml:"indexer"`

	Snapshot struct {
		// Deadline — общий дедлайн параллельного сбора из источников
		Deadline time.Duration `yaml:"deadline"`
		// AnomalyThresholdPct — отклонение от вчера (в %), после которого метрика помечается anomaly
		AnomalyThresholdPct float64 `yaml:"anomaly_threshold_pct"`
		// MaxEvents / MaxAlerts — усечение списков в ответе (лимит 100 KB)
		MaxEvents int `yaml:"max_events"`
		MaxAlerts int `yaml:"max_alerts"`
		// DefaultMetrics — golden signals, когда в service.yaml нет metrics. Плейсхолдеры в promql:
		// {namespace}, {pod_regex} (^(w1|w2)-.*), {service}
		DefaultMetrics []MetricDef `yaml:"default_metrics"`
		// PublicMetrics — внешняя картина по метрикам gateway ruto; добавляются к метрикам сервиса,
		// у которого есть приложения ruto. Плейсхолдер {ruto_apps} — регэксп имён приложений (a|b)
		PublicMetrics []MetricDef `yaml:"public_metrics"`
	} `yaml:"snapshot"`

	Logs struct {
		// MaxLines — сколько строк забирать из Loki за один запрос (лимит объёма выдачи)
		MaxLines int `yaml:"max_lines"`
		// MaxPatterns — top-N паттернов в ответе
		MaxPatterns int `yaml:"max_patterns"`
		// RawLimit — потолок строк в режиме raw
		RawLimit int `yaml:"raw_limit"`
		// MaxWindow — потолок окна запроса логов
		MaxWindow time.Duration `yaml:"max_window"`
		// TopErrors — сколько error-паттернов класть в снапшот
		TopErrors int `yaml:"top_errors"`
		// DefaultSelector — LogQL-селектор, когда в service.yaml нет logs.selector.
		// Плейсхолдеры: {namespace}, {pod_regex}, {service}, {workloads} (w1|w2)
		DefaultSelector string `yaml:"default_selector"`
		// ClusterSelector — LogQL-селектор всех логов кластера: поиск по всем сервисам
		// (query_logs без service) и ошибки кластера в get_cluster_health
		ClusterSelector string `yaml:"cluster_selector"`
		// Retention — сколько Loki хранит логи (retention_period): глубже искать нечего
		Retention time.Duration `yaml:"retention"`
		// SearchBudget — время на поиск по всем сервисам назад по суткам
		SearchBudget time.Duration `yaml:"search_budget"`
	} `yaml:"logs"`

	Timeline struct {
		Deadline     time.Duration `yaml:"deadline"`
		MaxEvents    int           `yaml:"max_events"`
		CommitsLimit int           `yaml:"commits_limit"`
		// MaxServicesForCommits — выше этого числа сервисов коммиты в таймлайн не собираются
		MaxServicesForCommits int `yaml:"max_services_for_commits"`
	} `yaml:"timeline"`

	Dependencies struct {
		MaxNodes int `yaml:"max_nodes"`
		MaxDepth int `yaml:"max_depth"`
	} `yaml:"dependencies"`

	Endpoints struct {
		// жёсткие потолки поверх декларации в service.yaml
		MaxRows      int           `yaml:"max_rows"`
		MaxBodyBytes int64         `yaml:"max_body_bytes"`
		MaxTimeout   time.Duration `yaml:"max_timeout"`
		// DefaultPort — порт ручки, если в декларации не задан (HTTP-порт сервиса)
		DefaultPort int `yaml:"default_port"`
	} `yaml:"endpoints"`

	Cluster struct {
		Deadline             time.Duration `yaml:"deadline"`
		PendingPodsThreshold int           `yaml:"pending_pods_threshold"`
		ProblemPodsThreshold int           `yaml:"problem_pods_threshold"`
		MaxProblemPods       int           `yaml:"max_problem_pods"`
		MaxEventReasons      int           `yaml:"max_event_reasons"`
		MaxInfraAlerts       int           `yaml:"max_infra_alerts"`
		// MaxLogServices — сколько сервисов с наибольшим числом ошибок в логах показывать
		MaxLogServices int `yaml:"max_log_services"`
		// Metrics — метрики кластера с базовой линией (node-exporter / kube-state-metrics)
		Metrics []MetricDef `yaml:"metrics"`
	} `yaml:"cluster"`

	Ruto struct {
		// GatewayService — сервис каталога gateway (имя образа ruto-gateway)
		GatewayService string `yaml:"gateway_service"`
		// RequestsMetric / DurationMetric — метрики gateway с лейблами app, method, status
		RequestsMetric string `yaml:"requests_metric"`
		DurationMetric string `yaml:"duration_metric"`
		// Deadline — дедлайн get_public_api; MaxEndpoints — потолок маршрутов в ответе
		Deadline     time.Duration `yaml:"deadline"`
		MaxEndpoints int           `yaml:"max_endpoints"`
	} `yaml:"ruto"`

	Metrics struct {
		// ограничения произвольного PromQL в query_metrics
		MaxWindow time.Duration `yaml:"max_window"`
		MaxSeries int           `yaml:"max_series"`
		MaxPoints int           `yaml:"max_points"`
	} `yaml:"metrics"`

	// правила «имя образа → репозиторий GitHub», применяются по порядку, первое совпадение.
	// Образ, не подошедший ни под одно правило, считается сторонним (postgres, redis…):
	// его workload попадает в каталог без репозитория.
	ImageMapping []ImageMapping `yaml:"image_mapping"`
}

// MetricDef — определение метрики (как в service.yaml).
type MetricDef struct {
	Id        string `yaml:"id"`
	Title     string `yaml:"title"`
	PromQL    string `yaml:"promql"`
	Unit      string `yaml:"unit"`
	Direction string `yaml:"direction"`
}

// ImageMapping — правило маппинга образа на репозиторий.
//
// Плейсхолдеры в repo_template: {repo} — первые два сегмента пути (mechta-market/promo-sync;
// ghcr.io кладёт образы внутрь репозитория: rendau/loom/server → rendau/loom), {path} — путь
// образа без registry целиком, {org} — первый сегмент пути (или поле org), {image_name} —
// последний сегмент пути.
type ImageMapping struct {
	Registry     string `yaml:"registry"`
	RepoTemplate string `yaml:"repo_template"`
	Org          string `yaml:"org"`
	// Path — маска пути образа (path.Match: rendau/ruto-*); пусто — любой путь registry.
	// Правила проверяются по порядку: исключения для образов, чьё имя не совпадает
	// с репозиторием, ставятся выше общего правила.
	Path string `yaml:"path"`
}

// LoadRules читает yaml по пути. Отсутствующий файл — не ошибка: возвращаются дефолты.
func LoadRules(path string) (*Rules, error) {
	rules := defaultRules()

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return rules, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	if err = yaml.Unmarshal(raw, rules); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	rules.applyDefaults()

	for _, m := range rules.ImageMapping {
		if _, err = filepath.Match(m.Path, ""); err != nil {
			return nil, fmt.Errorf("image_mapping: path %q: %w", m.Path, err)
		}
	}

	return rules, nil
}

func (r *Rules) applyDefaults() {
	if r.Indexer.StaleAfter <= 0 {
		r.Indexer.StaleAfter = time.Hour
	}
	if r.Snapshot.Deadline <= 0 {
		r.Snapshot.Deadline = 5 * time.Second
	}
	if r.Snapshot.AnomalyThresholdPct <= 0 {
		r.Snapshot.AnomalyThresholdPct = 30
	}
	if r.Snapshot.MaxEvents <= 0 {
		r.Snapshot.MaxEvents = 50
	}
	if r.Snapshot.MaxAlerts <= 0 {
		r.Snapshot.MaxAlerts = 50
	}
	if len(r.Snapshot.DefaultMetrics) == 0 {
		r.Snapshot.DefaultMetrics = defaultMetrics()
	}
	if r.Ruto.GatewayService == "" {
		r.Ruto.GatewayService = "ruto-gateway"
	}
	if r.Ruto.RequestsMetric == "" {
		r.Ruto.RequestsMetric = "mechta_ruto_gw_http_requests_total"
	}
	if r.Ruto.DurationMetric == "" {
		r.Ruto.DurationMetric = "mechta_ruto_gw_http_request_duration_seconds"
	}
	if r.Ruto.Deadline <= 0 {
		r.Ruto.Deadline = 8 * time.Second
	}
	if r.Ruto.MaxEndpoints <= 0 {
		r.Ruto.MaxEndpoints = 100
	}
	if len(r.Snapshot.PublicMetrics) == 0 {
		r.Snapshot.PublicMetrics = defaultPublicMetrics(r.Ruto.RequestsMetric, r.Ruto.DurationMetric)
	}
	if r.Logs.MaxLines <= 0 {
		r.Logs.MaxLines = 5000
	}
	if r.Logs.MaxPatterns <= 0 {
		r.Logs.MaxPatterns = 20
	}
	if r.Logs.RawLimit <= 0 {
		r.Logs.RawLimit = 100
	}
	if r.Logs.MaxWindow <= 0 {
		r.Logs.MaxWindow = 24 * time.Hour
	}
	if r.Logs.TopErrors <= 0 {
		r.Logs.TopErrors = 3
	}
	if r.Logs.DefaultSelector == "" {
		r.Logs.DefaultSelector = `{kubernetes_namespace_name="{namespace}", kubernetes_pod_name=~"{pod_regex}"}`
	}
	if r.Logs.Retention <= 0 {
		r.Logs.Retention = 720 * time.Hour
	}
	if r.Logs.SearchBudget <= 0 {
		r.Logs.SearchBudget = time.Minute
	}
	if r.Logs.ClusterSelector == "" {
		r.Logs.ClusterSelector = `{kubernetes_namespace_name=~".+"}`
	}
	if r.Timeline.Deadline <= 0 {
		r.Timeline.Deadline = 8 * time.Second
	}
	if r.Timeline.MaxEvents <= 0 {
		r.Timeline.MaxEvents = 100
	}
	if r.Timeline.CommitsLimit <= 0 {
		r.Timeline.CommitsLimit = 100
	}
	if r.Timeline.MaxServicesForCommits <= 0 {
		r.Timeline.MaxServicesForCommits = 10
	}
	if r.Dependencies.MaxNodes <= 0 {
		r.Dependencies.MaxNodes = 50
	}
	if r.Dependencies.MaxDepth <= 0 {
		r.Dependencies.MaxDepth = 3
	}
	if r.Endpoints.MaxRows <= 0 {
		r.Endpoints.MaxRows = 100
	}
	if r.Endpoints.MaxBodyBytes <= 0 {
		r.Endpoints.MaxBodyBytes = 256 << 10
	}
	if r.Endpoints.MaxTimeout <= 0 {
		r.Endpoints.MaxTimeout = 10 * time.Second
	}
	if r.Endpoints.DefaultPort <= 0 {
		r.Endpoints.DefaultPort = 80
	}
	if r.Cluster.Deadline <= 0 {
		r.Cluster.Deadline = time.Minute
	}
	if r.Cluster.PendingPodsThreshold <= 0 {
		r.Cluster.PendingPodsThreshold = 5
	}
	if r.Cluster.ProblemPodsThreshold <= 0 {
		r.Cluster.ProblemPodsThreshold = 3
	}
	if r.Cluster.MaxProblemPods <= 0 {
		r.Cluster.MaxProblemPods = 50
	}
	if r.Cluster.MaxEventReasons <= 0 {
		r.Cluster.MaxEventReasons = 15
	}
	if r.Cluster.MaxInfraAlerts <= 0 {
		r.Cluster.MaxInfraAlerts = 30
	}
	if r.Cluster.MaxLogServices <= 0 {
		r.Cluster.MaxLogServices = 10
	}
	if len(r.Cluster.Metrics) == 0 {
		r.Cluster.Metrics = defaultClusterMetrics()
	}
	if r.Metrics.MaxWindow <= 0 {
		r.Metrics.MaxWindow = 7 * 24 * time.Hour
	}
	if r.Metrics.MaxSeries <= 0 {
		r.Metrics.MaxSeries = 20
	}
	if r.Metrics.MaxPoints <= 0 {
		r.Metrics.MaxPoints = 200
	}
}

func defaultRules() *Rules {
	rules := &Rules{}
	rules.Indexer.ExcludeNamespaces = []string{"kube-system", "kube-public", "kube-node-lease"}
	rules.ImageMapping = []ImageMapping{
		{Registry: "ghcr.io", RepoTemplate: "https://github.com/{repo}"},
	}
	rules.applyDefaults()
	return rules
}

// defaultMetrics — golden signals по метрикам kubelet/cAdvisor и kube-state-metrics плюс
// http/grpc-метрики go-шаблона. Шаблон регистрирует их с префиксом namespace_subsystem
// (mechta_caravan_request_total, у старых сервисов — *_request_count) и статусом ok|error,
// поэтому имя ищется регэкспом, а ошибка — это status error или 5xx. Метрики приложения
// есть только у сервисов со ServiceMonitor; у остальных rps/error_rate/latency пустые.
//
// Под регэксп попадает несколько метрик (app_request_count и worker_request_count): rate()
// отбрасывает имя, и ряды с одинаковыми лейблами дают «vector cannot contain metrics with the
// same labelset». Поэтому имя копируется в лейбл metric (label_replace) и rate берётся по
// подзапросу [5m:30s] — label_replace работает только с мгновенным вектором.
func defaultMetrics() []MetricDef {
	const (
		requests = `{__name__=~".+_request_(total|count)", namespace="{namespace}", pod=~"{pod_regex}"}`
		errors   = `{__name__=~".+_request_(total|count)", namespace="{namespace}", pod=~"{pod_regex}", status=~"error|5.."}`
		buckets  = `{__name__=~".+_response_duration_seconds_bucket", namespace="{namespace}", pod=~"{pod_regex}"}`
	)
	rate := func(selector string) string {
		return `rate(label_replace(` + selector + `, "metric", "$1", "__name__", "(.+)")[5m:30s])`
	}
	return []MetricDef{
		{Id: "rps", Title: "Запросов в секунду", Unit: "rps",
			PromQL: `sum(` + rate(requests) + `)`},
		{Id: "error_rate", Title: "Доля ошибок (status error или 5xx)", Unit: "ratio", Direction: "lower_is_better",
			PromQL: `(sum(` + rate(errors) + `) or vector(0)) / sum(` + rate(requests) + `)`},
		{Id: "latency_p95", Title: "Latency p95", Unit: "seconds", Direction: "lower_is_better",
			PromQL: `histogram_quantile(0.95, sum by (le) (` + rate(buckets) + `))`},
		{Id: "cpu_cores", Title: "CPU, ядер", Unit: "cores", Direction: "lower_is_better",
			PromQL: `sum(rate(container_cpu_usage_seconds_total{namespace="{namespace}", pod=~"{pod_regex}", container!=""}[5m]))`},
		{Id: "memory_bytes", Title: "Память (working set)", Unit: "bytes", Direction: "lower_is_better",
			PromQL: `sum(container_memory_working_set_bytes{namespace="{namespace}", pod=~"{pod_regex}", container!=""})`},
		{Id: "restarts_1h", Title: "Рестарты контейнеров за час", Unit: "count", Direction: "lower_is_better",
			PromQL: `sum(increase(kube_pod_container_status_restarts_total{namespace="{namespace}", pod=~"{pod_regex}"}[1h]))`},
	}
}

// defaultPublicMetrics — трафик сервиса через gateway ruto: внешняя картина в дополнение к
// внутренним метрикам (расхождение между ними — сам по себе сигнал). Ошибка — 5xx или
// серверный код gRPC.
func defaultPublicMetrics(requestsMetric, durationMetric string) []MetricDef {
	requests := requestsMetric + `{app=~"{ruto_apps}"}`
	errors := requestsMetric + `{app=~"{ruto_apps}", status=~"5..|Internal|Unknown|Unavailable|DeadlineExceeded|ResourceExhausted|DataLoss|Unimplemented"}`
	return []MetricDef{
		{Id: "public_rps", Title: "Запросов в секунду через gateway", Unit: "rps",
			PromQL: `sum(rate(` + requests + `[5m]))`},
		{Id: "public_error_rate", Title: "Доля ошибок через gateway (5xx)", Unit: "ratio", Direction: "lower_is_better",
			PromQL: `(sum(rate(` + errors + `[5m])) or vector(0)) / sum(rate(` + requests + `[5m]))`},
		{Id: "public_latency_p95", Title: "Latency p95 через gateway", Unit: "seconds", Direction: "lower_is_better",
			PromQL: `histogram_quantile(0.95, sum by (le) (rate(` + durationMetric + `_bucket{app=~"{ruto_apps}"}[5m])))`},
	}
}

// defaultClusterMetrics — загрузка кластера по node-exporter и kube-state-metrics. Не ready
// считаются только живые поды (Pending/Running): завершившиеся поды Job'ов ready=false навсегда.
func defaultClusterMetrics() []MetricDef {
	return []MetricDef{
		{Id: "cluster_cpu_usage_ratio", Title: "Загрузка CPU кластера", Unit: "ratio", Direction: "lower_is_better",
			PromQL: `1 - avg(rate(node_cpu_seconds_total{mode="idle"}[5m]))`},
		{Id: "cluster_memory_usage_ratio", Title: "Использование памяти кластера", Unit: "ratio", Direction: "lower_is_better",
			PromQL: `1 - sum(node_memory_MemAvailable_bytes) / sum(node_memory_MemTotal_bytes)`},
		{Id: "pods_not_ready", Title: "Подов не ready", Unit: "count", Direction: "lower_is_better",
			PromQL: `sum(kube_pod_status_ready{condition="false"} * on(namespace, pod) group_left() (kube_pod_status_phase{phase=~"Pending|Running"} == 1))`},
		{Id: "container_restarts_1h", Title: "Рестарты контейнеров за час", Unit: "count", Direction: "lower_is_better",
			PromQL: `sum(increase(kube_pod_container_status_restarts_total[1h]))`},
	}
}
