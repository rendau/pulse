package dto

import (
	"time"

	"github.com/samber/lo"

	usecaseDependenciesModel "github.com/mechta-market/pulse/internal/usecase/dependencies/model"
)

type GetDependenciesReq struct {
	Service   string `json:"service" jsonschema:"точное имя сервиса"`
	Direction string `json:"direction,omitempty" jsonschema:"upstream — от кого зависит; downstream — кто зависит от него; both (по умолчанию)"`
	Depth     int    `json:"depth,omitempty" jsonschema:"глубина обхода, по умолчанию 1, максимум 3; ответ ограничен 50 узлами"`
}

type DependenciesRep struct {
	Service   string        `json:"service"`
	Direction string        `json:"direction"`
	Depth     int           `json:"depth"`
	Nodes     []GraphNode   `json:"nodes"`
	Edges     []GraphEdge   `json:"edges" jsonschema:"сконфигурированные связи (env/configmap, маршруты ruto), не фактический трафик"`
	Truncated bool          `json:"truncated" jsonschema:"true — узлов больше лимита, уменьши depth"`
	Errors    []SourceError `json:"errors"`
}

type GraphNode struct {
	Name     string `json:"name"`
	Title    string `json:"title,omitempty"`
	External bool   `json:"external,omitempty" jsonschema:"внешний адрес, не сервис каталога"`
	Health   string `json:"health,omitempty" jsonschema:"healthy | degraded | down | unknown — по подам, без полного снапшота"`
	Pods     string `json:"pods,omitempty"`
	Distance int    `json:"distance"`
}

type GraphEdge struct {
	From     string    `json:"from"`
	To       string    `json:"to"`
	Host     string    `json:"host"`
	Port     int32     `json:"port,omitempty"`
	Scheme   string    `json:"scheme,omitempty"`
	Source   string    `json:"source" jsonschema:"env | configmap | kusec | ruto (маршрут gateway, keys — имена приложений ruto) — откуда известна связь"`
	Keys     []string  `json:"keys" jsonschema:"переменные/ключи конфигурации с этим адресом"`
	LastSeen time.Time `json:"last_seen"`
}

func EncodeDependenciesRep(v *usecaseDependenciesModel.Graph) DependenciesRep {
	return DependenciesRep{
		Service:   v.Service,
		Direction: v.Direction,
		Depth:     v.Depth,
		Nodes: lo.Map(v.Nodes, func(n usecaseDependenciesModel.Node, _ int) GraphNode {
			return GraphNode{Name: n.Name, Title: n.Title, External: n.External, Health: n.Health, Pods: n.PodsInfo, Distance: n.Distance}
		}),
		Edges: lo.Map(v.Edges, func(e usecaseDependenciesModel.Edge, _ int) GraphEdge {
			return GraphEdge{From: e.From, To: e.To, Host: e.Host, Port: e.Port, Scheme: e.Scheme, Source: e.Source, Keys: e.Keys, LastSeen: e.LastSeen.UTC()}
		}),
		Truncated: v.Truncated,
		Errors: lo.Map(v.Errors, func(e usecaseDependenciesModel.SourceError, _ int) SourceError {
			return SourceError{Source: e.Source, Message: e.Message}
		}),
	}
}
