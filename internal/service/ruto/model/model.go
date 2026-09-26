package model

import "strings"

// типы маршрутов
const (
	EndpointTypeHttp = "http"
	EndpointTypeGrpc = "grpc"
)

// Snapshot — опубликованная конфигурация gateway.
type Snapshot struct {
	// Version — хэш содержимого; меняется при любой публикации конфигурации
	Version string
	// BaseUrl — внешний адрес gateway (домен)
	BaseUrl string
	Apps    []App
}

// App — приложение gateway: префикс публичного пути и backend внутри кластера.
type App struct {
	Id                 string
	Name               string
	Active             bool
	PathPrefix         string // /ocenter
	BackendUrl         string // http://ocenter.default.svc:80
	GrpcUrl            string // ocenter:5050
	ExcludeFromMetrics bool
	Endpoints          []Endpoint
}

// Endpoint — опубликованный маршрут приложения.
type Endpoint struct {
	Id     string
	Active bool
	Type   string // http | grpc
	Method string // GET | POST | * (http)
	Path   string // путь без префикса приложения: order/{id} (http)
	// GrpcPath — /pkg.Service/Method (grpc)
	GrpcPath string
}

// Route — маршрут в том виде, в каком он попадает в лейбл method метрик gateway:
// «GET /ocenter/order/{id}» для http, «GRPC (app)/pkg.Svc/Method» для grpc.
func (a App) Route(e Endpoint) string {
	if e.Type == EndpointTypeGrpc {
		return "GRPC (" + a.Name + ")" + e.GrpcPath
	}
	path := a.PathPrefix
	if p := strings.Trim(e.Path, "/"); p != "" {
		path += "/" + p
	}
	return e.Method + " " + path
}

// виды ошибок gateway из его логов
const (
	// GatewayErrorProxy — backend не ответил: отказ соединения, таймаут, хост не найден (502)
	GatewayErrorProxy = "proxy"
	// GatewayErrorScript — скрипт трансформации маршрута не компилируется или падает
	GatewayErrorScript = "script"
)

// GatewayError — ошибка из лога gateway: backend не ответил или сломан скрипт трансформации.
type GatewayError struct {
	Kind string // GatewayErrorProxy | GatewayErrorScript
	// AppName — приложение (ошибки backend'а); AppId, EndpointId — маршрут (ошибки скрипта)
	AppName    string
	AppId      string
	EndpointId string
	// Reason — причина по-человечески: «backend connection refused», «request transform: compile failed»
	Reason string
	// Error — текст ошибки как есть
	Error string
}
