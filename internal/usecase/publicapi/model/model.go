package model

import "time"

// PublicApi — внешний контур сервиса через gateway ruto: домен, опубликованные маршруты и
// трафик за окно по метрикам gateway.
type PublicApi struct {
	Service string
	Window  time.Duration
	// BaseUrl — внешний адрес gateway
	BaseUrl string
	Apps    []App
	Traffic *Traffic
	// Routes — маршруты всех приложений сервиса, по убыванию трафика
	Routes     []Route
	TotalCount int
	Truncated  bool
	Errors     []SourceError
}

// App — приложение gateway, ведущее на сервис.
type App struct {
	Name       string
	PathPrefix string
	BackendUrl string
	GrpcUrl    string
	// Endpoints / InactiveEndpoints — число активных и выключенных маршрутов
	Endpoints         int
	InactiveEndpoints int
}

// Traffic — сводка трафика за окно.
type Traffic struct {
	Rps       float64
	Requests  float64
	ErrorRate *float64
	P95       *float64
	// Statuses — распределение кодов ответа, по убыванию
	Statuses []StatusShare
}

type StatusShare struct {
	Status   string
	Requests float64
	Share    float64
}

// Route — маршрут gateway с трафиком за окно. Configured=false — трафик есть в метриках, но
// маршрута нет в текущей конфигурации (удалён или переименован за окно).
type Route struct {
	App        string
	Route      string // GET /ocenter/order/{id}
	Type       string // http | grpc
	Configured bool
	Requests   float64
	Rps        float64
	ErrorRate  *float64
	P95        *float64
	// Errors — число ответов 5xx / серверных кодов gRPC
	Errors float64
}

type SourceError struct {
	Source  string
	Message string
}
