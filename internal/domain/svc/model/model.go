package model

import (
	"time"

	commonModel "github.com/mechta-market/pulse/internal/domain/common/model"
)

// Main — сервис каталога: один репозиторий, один или несколько workload'ов.
type Main struct {
	Name          string
	Title         string
	RepoUrl       string
	Description   string
	Criticality   string
	OwnerTeam     string
	OwnerContacts []string
	Aliases       []string
	// MetadataPresent — в репозитории есть service.yaml
	MetadataPresent bool
	Metadata        Metadata
	FirstSeen       time.Time
	LastSeen        time.Time
}

// Edit — мутация (все поля pointer-типы для partial update)
type Edit struct {
	Name            *string
	Title           *string
	RepoUrl         *string
	Description     *string
	Criticality     *string
	OwnerTeam       *string
	OwnerContacts   *[]string
	Aliases         *[]string
	MetadataPresent *bool
	Metadata        *Metadata
	// FirstSeen пишется только при вставке; при обновлении не трогается
	FirstSeen *time.Time
	LastSeen  *time.Time
}

// ListReq — параметры выборки
type ListReq struct {
	commonModel.ListParams

	Names       []string
	Team        *string
	Namespace   *string
	Criticality *string
	HasMetadata *bool
	RepoUrl     *string
	// Search — подстрока в name/title/aliases (ILIKE)
	Search *string
}

// Metadata — часть service.yaml, которую нельзя вывести из кластера:
// семантика метрик, логов, раннбуки, диагностические ручки.
type Metadata struct {
	Metrics   []Metric
	Logs      Logs
	Runbooks  []Runbook
	Endpoints []Endpoint
}

type Metric struct {
	Id        string
	Title     string
	PromQL    string
	Unit      string
	Direction string // higher_is_better | lower_is_better
}

type Logs struct {
	Selector      string
	ErrorPatterns []ErrorPattern
}

type ErrorPattern struct {
	Name    string
	Pattern string
}

type Runbook struct {
	Title string
	Url   string
}

// Endpoint — декларация диагностической ручки (фаза 6). Вызов возможен только по id.
type Endpoint struct {
	Id      string
	Title   string
	Path    string
	Method  string
	Params  map[string]EndpointParam
	MaxRows int
	PII     []string
	Timeout time.Duration
}

type EndpointParam struct {
	Type     string
	Default  string
	Max      *float64
	Min      *float64
	Required bool
}

// Candidate — результат резолвинга человеческой формулировки в сервис.
type Candidate struct {
	Service    *Main
	Confidence float64
	MatchedBy  string
}
