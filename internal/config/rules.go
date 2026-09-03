package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
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
	} `yaml:"snapshot"`

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
// Плейсхолдеры в repo_template: {path} — путь образа без registry
// (mechta-market/promo-sync), {org} — первый сегмент пути (или поле org),
// {image_name} — последний сегмент пути.
type ImageMapping struct {
	Registry     string `yaml:"registry"`
	RepoTemplate string `yaml:"repo_template"`
	Org          string `yaml:"org"`
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
		{Registry: "ghcr.io", RepoTemplate: "https://github.com/{path}"},
	}
	rules.applyDefaults()
	return rules
}

// defaultMetrics — golden signals по метрикам kubelet/cAdvisor и kube-state-metrics плюс
// http-метрики go-шаблона (request_total / response_duration_seconds). Имена метрик приложения
// в конкретном кластере могут отличаться — тогда набор переопределяется в conf.yml.
func defaultMetrics() []MetricDef {
	return []MetricDef{
		{Id: "rps", Title: "Запросов в секунду", Unit: "rps",
			PromQL: `sum(rate(request_total{namespace="{namespace}", pod=~"{pod_regex}"}[5m]))`},
		{Id: "error_rate_5xx", Title: "Доля ответов 5xx", Unit: "ratio", Direction: "lower_is_better",
			PromQL: `sum(rate(request_total{namespace="{namespace}", pod=~"{pod_regex}", status=~"5.."}[5m])) / sum(rate(request_total{namespace="{namespace}", pod=~"{pod_regex}"}[5m]))`},
		{Id: "latency_p95", Title: "Latency p95", Unit: "seconds", Direction: "lower_is_better",
			PromQL: `histogram_quantile(0.95, sum by (le) (rate(response_duration_seconds_bucket{namespace="{namespace}", pod=~"{pod_regex}"}[5m])))`},
		{Id: "cpu_cores", Title: "CPU, ядер", Unit: "cores", Direction: "lower_is_better",
			PromQL: `sum(rate(container_cpu_usage_seconds_total{namespace="{namespace}", pod=~"{pod_regex}", container!=""}[5m]))`},
		{Id: "memory_bytes", Title: "Память (working set)", Unit: "bytes", Direction: "lower_is_better",
			PromQL: `sum(container_memory_working_set_bytes{namespace="{namespace}", pod=~"{pod_regex}", container!=""})`},
		{Id: "restarts_1h", Title: "Рестарты контейнеров за час", Unit: "count", Direction: "lower_is_better",
			PromQL: `sum(increase(kube_pod_container_status_restarts_total{namespace="{namespace}", pod=~"{pod_regex}"}[1h]))`},
	}
}
