package model

import "time"

// Config — параметры индексера.
type Config struct {
	Cluster    string
	Interval   time.Duration
	StaleAfter time.Duration
	// ImageMapping — правила «образ → репозиторий», первое совпадение
	ImageMapping []ImageMapping
	// RutoGatewayService — сервис каталога gateway ruto: источник рёбер «gateway → backend приложения»
	RutoGatewayService string
	// Manifest — поиск манифеста сервиса через его k8s Service (docs/service-manifest.md)
	Manifest ManifestConfig
}

// ManifestConfig — поиск манифеста сервиса.
type ManifestConfig struct {
	// Path — путь манифеста (ручка состояния — Path/status)
	Path string
	// ServicePort — имя порта k8s Service workload'а, через который pulse вызывает манифест
	ServicePort string
	// RefreshAfter — перечитать принятый манифест без выкатки; RetryAfter — повтор после неудачи
	RefreshAfter time.Duration
	RetryAfter   time.Duration
}

type ImageMapping struct {
	Registry     string
	RepoTemplate string
	Org          string
	// Path — маска пути образа (path.Match); пусто — любой путь registry
	Path string
}

// Cycle — последний цикл индексера (диагностическая ручка pulse).
type Cycle struct {
	FinishedAt        time.Time
	Duration          time.Duration
	Workloads         int
	Services          int
	WithMetadata      int
	WithManifest      int
	MetadataErrors    int
	CommitsResolved   int
	GithubUnavailable bool
	// Error — цикл не завершился (текст для человека); пусто — завершился
	Error string
}
