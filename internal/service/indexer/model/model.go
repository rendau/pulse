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
	// Manifest — поиск манифеста сервиса на подах (docs/service-manifest.md)
	Manifest ManifestConfig
}

// ManifestConfig — поиск манифеста сервиса.
type ManifestConfig struct {
	// Path — путь манифеста (ручка состояния — Path/status)
	Path string
	// DefaultPorts — порты, если нет аннотации, цели Prometheus и портов с именами system/http
	DefaultPorts []int
	// AnnotationPrefix — префикс аннотаций пода: <prefix>port, <prefix>path
	AnnotationPrefix string
	// RefreshAfter — перечитать принятый манифест без выкатки; RetryAfter — повтор после неудачи
	RefreshAfter time.Duration
	RetryAfter   time.Duration
	// SkipPorts — заведомо не-HTTP порты: при переборе остальных портов пода не трогаются
	SkipPorts []int
}

type ImageMapping struct {
	Registry     string
	RepoTemplate string
	Org          string
	// Path — маска пути образа (path.Match); пусто — любой путь registry
	Path string
}
