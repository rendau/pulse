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
}

type ImageMapping struct {
	Registry     string
	RepoTemplate string
	Org          string
	// Path — маска пути образа (path.Match); пусто — любой путь registry
	Path string
}
