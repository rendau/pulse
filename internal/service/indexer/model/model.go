package model

import "time"

// Config — параметры индексера.
type Config struct {
	Cluster    string
	Interval   time.Duration
	StaleAfter time.Duration
	// ImageMapping — правила «образ → репозиторий», первое совпадение
	ImageMapping []ImageMapping
}

type ImageMapping struct {
	Registry     string
	RepoTemplate string
	Org          string
}
