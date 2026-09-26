package model

import (
	"time"

	commonModel "github.com/rendau/pulse/internal/domain/common/model"
)

// источники факта связи
const (
	SourceEnv       = "env"
	SourceConfigMap = "configmap"
	SourceKusec     = "kusec"
	// SourceRuto — маршрут gateway ruto на backend приложения
	SourceRuto = "ruto"
)

// Main — сконфигурированная связь «from_service → to_host» (не фактический трафик).
type Main struct {
	Cluster     string
	FromService string
	// ToService — сервис каталога, в который резолвится хост; пустой — внешний адрес
	ToService string
	ToHost    string
	Port      int32
	Scheme    string
	Source    string // env | configmap | kusec | ruto
	Key       string // имя переменной / ключа; для ruto — имя приложения gateway
	FirstSeen time.Time
	LastSeen  time.Time
}

// Edit — мутация (все поля pointer-типы)
type Edit struct {
	Cluster     *string
	FromService *string
	ToService   *string
	ToHost      *string
	Port        *int32
	Scheme      *string
	Source      *string
	Key         *string
	FirstSeen   *time.Time
	LastSeen    *time.Time
}

// ListReq — параметры выборки
type ListReq struct {
	commonModel.ListParams

	Cluster      *string
	FromServices []string
	ToServices   []string
}

// Endpoint — разобранный адрес из значения конфигурации (без учётных данных).
type Endpoint struct {
	Scheme string
	Host   string
	Port   int32
}
