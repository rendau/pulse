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
	// ClusterNames — имена сервиса в кластере (workload'ы, k8s Service, приложения ruto),
	// выводятся индексером; в отличие от Aliases не требуют service.yaml
	ClusterNames []string
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
	ClusterNames    *[]string
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

// источники метаданных сервиса
const (
	MetadataSourceManifest    = "manifest"     // манифест сервиса (docs/service-manifest.md)
	MetadataSourceServiceYaml = "service_yaml" // service.yaml в репозитории
)

// Metadata — то, что нельзя вывести из кластера: семантика метрик, логов, раннбуки,
// зависимости, диагностические ручки. Из манифеста сервиса, иначе из service.yaml.
type Metadata struct {
	// Source — manifest | service_yaml; пусто — метаданных нет
	Source       string
	Metrics      []Metric
	Logs         Logs
	Runbooks     []Runbook
	Endpoints    []Endpoint
	Dependencies []Dependency
	DocsUrl      string
	// Domain — бизнес-смысл сервиса со слов владельца (только из манифеста); nil — не описан
	Domain *Domain
}

// Domain — за что сервис отвечает, чего не делает, с какими объектами работает и какие вопросы
// к нему типичны: агенту — понять, тот ли это сервис и как читать его данные.
type Domain struct {
	Responsibilities []string
	NotResponsible   []Boundary
	Entities         []Entity
	Questions        []Question
}

// Boundary — чем сервис не занимается и кто занимается (имя сервиса каталога, если известно).
type Boundary struct {
	What    string
	Service string
}

// Entity — бизнес-объект сервиса: как выглядит его номер (IdPattern — RE2 на всё значение),
// что значат статусы и когда объект считается застрявшим.
type Entity struct {
	Name        string
	IdPattern   string
	IdExample   string
	Description string
	Statuses    []EntityStatus
}

type EntityStatus struct {
	Name    string
	Meaning string
	// StuckAfter — дольше в этом статусе — застрял; 0 — не задано
	StuckAfter time.Duration
}

// Question — типичный вопрос к сервису и куда за ответом (Endpoint — id ручки манифеста).
type Question struct {
	Question string
	How      string
	Endpoint string
}

// Dependency — зависимость, которую сервис объявил сам: основа ручки состояния.
type Dependency struct {
	Id     string
	Kind   string // postgres | redis | kafka | … | http | grpc | other
	Target string // имя сервиса в кластере или внешний хост, без учётных данных
	// Critical — без неё сервис не работает (её down — сервис down)
	Critical bool
	// Affects — что ломается, когда она недоступна («выдача заказов»), со слов владельца
	Affects string
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

// Endpoint — диагностическая ручка из манифеста сервиса. Вызов возможен только по id, только GET.
type Endpoint struct {
	Id          string
	Title       string
	Description string // для агента: когда вызывать
	Path        string
	// Workload — workload, объявивший ручку в манифесте: вызов — прямо в его под, на порт манифеста
	Workload *WorkloadRef
	Params   map[string]EndpointParam
	// Response — схема ответа из манифеста: к агенту доходят только объявленные поля
	Response *Schema
	// RowsPath — где в ответе список (для лимита строк); пусто — сам ответ, если это массив
	RowsPath string
	MaxRows  int
	Timeout  time.Duration
}

// WorkloadRef — workload, который обслуживает ручку.
type WorkloadRef struct {
	Namespace string
	Kind      string
	Name      string
}

type EndpointParam struct {
	Type        string
	Default     string
	Max         *float64
	Min         *float64
	Required    bool
	Pattern     string
	Enum        []string
	Description string
	// Personal — вид персональных данных (phone, email…): параметр принимает токен
	Personal string
}

// Schema — схема ответа ручки (подмножество JSON Schema из манифеста).
type Schema struct {
	Type       string // object | array | string | integer | number | boolean
	Properties map[string]*Schema
	Items      *Schema
	// Values — схема значений словаря (additionalProperties): только number | integer | boolean
	Values      *Schema
	Enum        []string
	Format      string
	MaxLength   int
	MaxItems    int
	Description string
	// Personal — вид персональных данных поля (phone, email…): значение заменяется токеном
	Personal string
}

// Candidate — результат резолвинга человеческой формулировки в сервис.
type Candidate struct {
	Service    *Main
	Confidence float64
	MatchedBy  string
}
