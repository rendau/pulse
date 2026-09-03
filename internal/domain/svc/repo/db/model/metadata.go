package model

import (
	"time"

	"github.com/samber/lo"

	domainModel "github.com/mechta-market/pulse/internal/domain/svc/model"
)

// metadataJSON — repo-локальная DTO формата хранения jsonb-колонки metadata.
// Только она знает про json-теги; доменная модель тегов не несёт.
type metadataJSON struct {
	Metrics   []metricJSON   `json:"metrics,omitempty"`
	Logs      logsJSON       `json:"logs"`
	Runbooks  []runbookJSON  `json:"runbooks,omitempty"`
	Endpoints []endpointJSON `json:"endpoints,omitempty"`
}

type metricJSON struct {
	Id        string `json:"id"`
	Title     string `json:"title"`
	PromQL    string `json:"promql"`
	Unit      string `json:"unit"`
	Direction string `json:"direction"`
}

type logsJSON struct {
	Selector      string             `json:"selector"`
	ErrorPatterns []errorPatternJSON `json:"error_patterns,omitempty"`
}

type errorPatternJSON struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
}

type runbookJSON struct {
	Title string `json:"title"`
	Url   string `json:"url"`
}

type endpointJSON struct {
	Id      string                       `json:"id"`
	Title   string                       `json:"title"`
	Path    string                       `json:"path"`
	Method  string                       `json:"method"`
	Port    int                          `json:"port,omitempty"`
	K8sSvc  string                       `json:"k8s_service,omitempty"`
	Params  map[string]endpointParamJSON `json:"params,omitempty"`
	MaxRows int                          `json:"max_rows"`
	PII     []string                     `json:"pii,omitempty"`
	Timeout int64                        `json:"timeout_ms"`
}

type endpointParamJSON struct {
	Type     string   `json:"type"`
	Default  string   `json:"default,omitempty"`
	Max      *float64 `json:"max,omitempty"`
	Min      *float64 `json:"min,omitempty"`
	Required bool     `json:"required,omitempty"`
}

// encode: json → domain

func encodeMetadata(v metadataJSON) domainModel.Metadata {
	return domainModel.Metadata{
		Metrics: lo.Map(v.Metrics, encodeMetric),
		Logs: domainModel.Logs{
			Selector:      v.Logs.Selector,
			ErrorPatterns: lo.Map(v.Logs.ErrorPatterns, encodeErrorPattern),
		},
		Runbooks:  lo.Map(v.Runbooks, encodeRunbook),
		Endpoints: lo.Map(v.Endpoints, encodeEndpoint),
	}
}

func encodeMetric(v metricJSON, _ int) domainModel.Metric {
	return domainModel.Metric{Id: v.Id, Title: v.Title, PromQL: v.PromQL, Unit: v.Unit, Direction: v.Direction}
}

func encodeErrorPattern(v errorPatternJSON, _ int) domainModel.ErrorPattern {
	return domainModel.ErrorPattern{Name: v.Name, Pattern: v.Pattern}
}

func encodeRunbook(v runbookJSON, _ int) domainModel.Runbook {
	return domainModel.Runbook{Title: v.Title, Url: v.Url}
}

func encodeEndpoint(v endpointJSON, _ int) domainModel.Endpoint {
	return domainModel.Endpoint{
		Id:         v.Id,
		Title:      v.Title,
		Path:       v.Path,
		Method:     v.Method,
		Port:       v.Port,
		K8sService: v.K8sSvc,
		Params: lo.MapValues(v.Params, func(p endpointParamJSON, _ string) domainModel.EndpointParam {
			return domainModel.EndpointParam{Type: p.Type, Default: p.Default, Max: p.Max, Min: p.Min, Required: p.Required}
		}),
		MaxRows: v.MaxRows,
		PII:     v.PII,
		Timeout: time.Duration(v.Timeout) * time.Millisecond,
	}
}

// decode: domain → json

func decodeMetadata(v *domainModel.Metadata) metadataJSON {
	return metadataJSON{
		Metrics: lo.Map(v.Metrics, decodeMetric),
		Logs: logsJSON{
			Selector:      v.Logs.Selector,
			ErrorPatterns: lo.Map(v.Logs.ErrorPatterns, decodeErrorPattern),
		},
		Runbooks:  lo.Map(v.Runbooks, decodeRunbook),
		Endpoints: lo.Map(v.Endpoints, decodeEndpoint),
	}
}

func decodeMetric(v domainModel.Metric, _ int) metricJSON {
	return metricJSON{Id: v.Id, Title: v.Title, PromQL: v.PromQL, Unit: v.Unit, Direction: v.Direction}
}

func decodeErrorPattern(v domainModel.ErrorPattern, _ int) errorPatternJSON {
	return errorPatternJSON{Name: v.Name, Pattern: v.Pattern}
}

func decodeRunbook(v domainModel.Runbook, _ int) runbookJSON {
	return runbookJSON{Title: v.Title, Url: v.Url}
}

func decodeEndpoint(v domainModel.Endpoint, _ int) endpointJSON {
	return endpointJSON{
		Id:     v.Id,
		Title:  v.Title,
		Path:   v.Path,
		Method: v.Method,
		Port:   v.Port,
		K8sSvc: v.K8sService,
		Params: lo.MapValues(v.Params, func(p domainModel.EndpointParam, _ string) endpointParamJSON {
			return endpointParamJSON{Type: p.Type, Default: p.Default, Max: p.Max, Min: p.Min, Required: p.Required}
		}),
		MaxRows: v.MaxRows,
		PII:     v.PII,
		Timeout: v.Timeout.Milliseconds(),
	}
}
