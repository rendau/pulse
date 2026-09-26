package dto

import (
	"github.com/samber/lo"

	usecasePublicapiModel "github.com/rendau/pulse/internal/usecase/publicapi/model"
	"github.com/rendau/pulse/internal/util/window"
)

type GetPublicApiReq struct {
	Service string `json:"service" jsonschema:"точное имя сервиса"`
	Window  string `json:"window,omitempty" jsonschema:"Go duration: 15m, 1h (по умолчанию), 24h, 7d (максимум)"`
}

type PublicApiRep struct {
	Service    string         `json:"service"`
	Window     string         `json:"window"`
	BaseUrl    string         `json:"base_url,omitempty" jsonschema:"внешний адрес gateway"`
	Apps       []PublicApp    `json:"apps" jsonschema:"приложения gateway, ведущие на сервис"`
	Traffic    *PublicTraffic `json:"traffic,omitempty" jsonschema:"трафик через gateway за окно; отсутствует, если метрики недоступны"`
	Routes     []PublicRoute  `json:"routes" jsonschema:"маршруты по убыванию запросов за окно"`
	TotalCount int            `json:"total_count"`
	Truncated  bool           `json:"truncated"`
	Errors     []SourceError  `json:"errors"`
}

type PublicApp struct {
	Name              string `json:"name"`
	PathPrefix        string `json:"path_prefix"`
	BackendUrl        string `json:"backend_url,omitempty"`
	GrpcUrl           string `json:"grpc_url,omitempty"`
	Endpoints         int    `json:"endpoints"`
	InactiveEndpoints int    `json:"inactive_endpoints,omitempty"`
}

type PublicTraffic struct {
	Rps       float64        `json:"rps"`
	Requests  float64        `json:"requests"`
	ErrorRate *float64       `json:"error_rate,omitempty" jsonschema:"доля 5xx и серверных кодов gRPC; 4xx — не ошибка сервера"`
	P95       *float64       `json:"p95_seconds,omitempty"`
	Statuses  []PublicStatus `json:"statuses"`
}

type PublicStatus struct {
	Status   string  `json:"status"`
	Requests float64 `json:"requests"`
	Share    float64 `json:"share"`
}

type PublicRoute struct {
	App        string   `json:"app,omitempty"`
	Route      string   `json:"route" jsonschema:"«GET /prefix/path» или «GRPC (app)/pkg.Service/Method»"`
	Type       string   `json:"type"`
	Configured bool     `json:"configured" jsonschema:"false — трафик был, но маршрута уже нет в конфигурации gateway"`
	Requests   float64  `json:"requests"`
	Rps        float64  `json:"rps"`
	Errors     float64  `json:"errors"`
	ErrorRate  *float64 `json:"error_rate,omitempty"`
	P95        *float64 `json:"p95_seconds,omitempty"`
}

func EncodePublicApiRep(v *usecasePublicapiModel.PublicApi) PublicApiRep {
	rep := PublicApiRep{
		Service: v.Service,
		Window:  window.Format(v.Window),
		BaseUrl: v.BaseUrl,
		Apps: lo.Map(v.Apps, func(a usecasePublicapiModel.App, _ int) PublicApp {
			return PublicApp{Name: a.Name, PathPrefix: a.PathPrefix, BackendUrl: a.BackendUrl, GrpcUrl: a.GrpcUrl,
				Endpoints: a.Endpoints, InactiveEndpoints: a.InactiveEndpoints}
		}),
		Routes:     lo.Map(v.Routes, encodePublicRoute),
		TotalCount: v.TotalCount,
		Truncated:  v.Truncated,
		Errors: lo.Map(v.Errors, func(e usecasePublicapiModel.SourceError, _ int) SourceError {
			return SourceError{Source: e.Source, Message: e.Message}
		}),
	}
	if v.Traffic != nil {
		rep.Traffic = &PublicTraffic{
			Rps: v.Traffic.Rps, Requests: v.Traffic.Requests, ErrorRate: v.Traffic.ErrorRate, P95: v.Traffic.P95,
			Statuses: lo.Map(v.Traffic.Statuses, func(s usecasePublicapiModel.StatusShare, _ int) PublicStatus {
				return PublicStatus{Status: s.Status, Requests: s.Requests, Share: s.Share}
			}),
		}
	}
	return rep
}

func encodePublicRoute(v usecasePublicapiModel.Route, _ int) PublicRoute {
	return PublicRoute{
		App: v.App, Route: v.Route, Type: v.Type, Configured: v.Configured,
		Requests: v.Requests, Rps: v.Rps, Errors: v.Errors, ErrorRate: v.ErrorRate, P95: v.P95,
	}
}
