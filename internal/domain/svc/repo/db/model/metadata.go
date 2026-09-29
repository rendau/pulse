package model

import (
	"time"

	"github.com/samber/lo"

	domainModel "github.com/rendau/pulse/internal/domain/svc/model"
)

// metadataJSON — repo-локальная DTO формата хранения jsonb-колонки metadata.
// Только она знает про json-теги; доменная модель тегов не несёт.
type metadataJSON struct {
	Source       string           `json:"source,omitempty"`
	Metrics      []metricJSON     `json:"metrics,omitempty"`
	Logs         logsJSON         `json:"logs"`
	Runbooks     []runbookJSON    `json:"runbooks,omitempty"`
	Endpoints    []endpointJSON   `json:"endpoints,omitempty"`
	Dependencies []dependencyJSON `json:"dependencies,omitempty"`
	DocsUrl      string           `json:"docs_url,omitempty"`
	Domain       *domainJSON      `json:"domain,omitempty"`
}

type domainJSON struct {
	Responsibilities []string       `json:"responsibilities,omitempty"`
	NotResponsible   []boundaryJSON `json:"not_responsible,omitempty"`
	Entities         []entityJSON   `json:"entities,omitempty"`
	Questions        []questionJSON `json:"questions,omitempty"`
}

type boundaryJSON struct {
	What    string `json:"what"`
	Service string `json:"service,omitempty"`
}

type entityJSON struct {
	Name        string             `json:"name"`
	IdPattern   string             `json:"id_pattern,omitempty"`
	IdExample   string             `json:"id_example,omitempty"`
	Description string             `json:"description,omitempty"`
	Statuses    []entityStatusJSON `json:"statuses,omitempty"`
}

type entityStatusJSON struct {
	Name       string `json:"name"`
	Meaning    string `json:"meaning,omitempty"`
	StuckAfter int64  `json:"stuck_after_s,omitempty"`
}

type questionJSON struct {
	Question string `json:"question"`
	How      string `json:"how,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
}

type dependencyJSON struct {
	Id       string `json:"id"`
	Kind     string `json:"kind"`
	Target   string `json:"target"`
	Service  string `json:"service,omitempty"`
	Critical bool   `json:"critical,omitempty"`
	Affects  string `json:"affects,omitempty"`
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
	Id          string                       `json:"id"`
	Title       string                       `json:"title"`
	Description string                       `json:"description,omitempty"`
	Path        string                       `json:"path"`
	Workload    *workloadRefJSON             `json:"workload,omitempty"`
	Audience    string                       `json:"audience,omitempty"`
	Params      map[string]endpointParamJSON `json:"params,omitempty"`
	Response    *schemaJSON                  `json:"response,omitempty"`
	RowsPath    string                       `json:"rows_path,omitempty"`
	MaxRows     int                          `json:"max_rows"`
	Timeout     int64                        `json:"timeout_ms"`
}

type workloadRefJSON struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
}

type schemaJSON struct {
	Type        string                 `json:"type"`
	Properties  map[string]*schemaJSON `json:"properties,omitempty"`
	Items       *schemaJSON            `json:"items,omitempty"`
	Values      *schemaJSON            `json:"values,omitempty"`
	Enum        []string               `json:"enum,omitempty"`
	Format      string                 `json:"format,omitempty"`
	MaxLength   int                    `json:"max_length,omitempty"`
	MaxItems    int                    `json:"max_items,omitempty"`
	Description string                 `json:"description,omitempty"`
	Personal    string                 `json:"personal,omitempty"`
}

type endpointParamJSON struct {
	Type        string   `json:"type"`
	Default     string   `json:"default,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	Min         *float64 `json:"min,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Pattern     string   `json:"pattern,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Description string   `json:"description,omitempty"`
	Personal    string   `json:"personal,omitempty"`
}

// encode: json → domain

func encodeMetadata(v metadataJSON) domainModel.Metadata {
	return domainModel.Metadata{
		Source:  v.Source,
		Metrics: lo.Map(v.Metrics, encodeMetric),
		Logs: domainModel.Logs{
			Selector:      v.Logs.Selector,
			ErrorPatterns: lo.Map(v.Logs.ErrorPatterns, encodeErrorPattern),
		},
		Runbooks:     lo.Map(v.Runbooks, encodeRunbook),
		Endpoints:    lo.Map(v.Endpoints, encodeEndpoint),
		Dependencies: lo.Map(v.Dependencies, encodeDependency),
		DocsUrl:      v.DocsUrl,
		Domain:       encodeDomain(v.Domain),
	}
}

func encodeDomain(v *domainJSON) *domainModel.Domain {
	if v == nil {
		return nil
	}
	return &domainModel.Domain{
		Responsibilities: v.Responsibilities,
		NotResponsible: lo.Map(v.NotResponsible, func(b boundaryJSON, _ int) domainModel.Boundary {
			return domainModel.Boundary{What: b.What, Service: b.Service}
		}),
		Entities: lo.Map(v.Entities, func(e entityJSON, _ int) domainModel.Entity {
			return domainModel.Entity{
				Name: e.Name, IdPattern: e.IdPattern, IdExample: e.IdExample, Description: e.Description,
				Statuses: lo.Map(e.Statuses, func(s entityStatusJSON, _ int) domainModel.EntityStatus {
					return domainModel.EntityStatus{Name: s.Name, Meaning: s.Meaning, StuckAfter: time.Duration(s.StuckAfter) * time.Second}
				}),
			}
		}),
		Questions: lo.Map(v.Questions, func(q questionJSON, _ int) domainModel.Question {
			return domainModel.Question{Question: q.Question, How: q.How, Endpoint: q.Endpoint}
		}),
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

func encodeDependency(v dependencyJSON, _ int) domainModel.Dependency {
	return domainModel.Dependency{Id: v.Id, Kind: v.Kind, Target: v.Target, Service: v.Service, Critical: v.Critical, Affects: v.Affects}
}

func encodeEndpoint(v endpointJSON, _ int) domainModel.Endpoint {
	result := domainModel.Endpoint{
		Id:          v.Id,
		Title:       v.Title,
		Description: v.Description,
		Path:        v.Path,
		Audience:    v.Audience,
		Params: lo.MapValues(v.Params, func(p endpointParamJSON, _ string) domainModel.EndpointParam {
			return domainModel.EndpointParam{
				Type: p.Type, Default: p.Default, Max: p.Max, Min: p.Min, Required: p.Required,
				Pattern: p.Pattern, Enum: p.Enum, Description: p.Description, Personal: p.Personal,
			}
		}),
		Response: encodeSchema(v.Response),
		RowsPath: v.RowsPath,
		MaxRows:  v.MaxRows,
		Timeout:  time.Duration(v.Timeout) * time.Millisecond,
	}
	if v.Workload != nil {
		result.Workload = &domainModel.WorkloadRef{Namespace: v.Workload.Namespace, Kind: v.Workload.Kind, Name: v.Workload.Name}
	}
	return result
}

func encodeSchema(v *schemaJSON) *domainModel.Schema {
	if v == nil {
		return nil
	}
	result := &domainModel.Schema{
		Type: v.Type, Items: encodeSchema(v.Items), Values: encodeSchema(v.Values), Enum: v.Enum, Format: v.Format,
		MaxLength: v.MaxLength, MaxItems: v.MaxItems, Description: v.Description, Personal: v.Personal,
	}
	if len(v.Properties) > 0 {
		result.Properties = lo.MapValues(v.Properties, func(p *schemaJSON, _ string) *domainModel.Schema { return encodeSchema(p) })
	}
	return result
}

// decode: domain → json

func decodeMetadata(v *domainModel.Metadata) metadataJSON {
	return metadataJSON{
		Source:  v.Source,
		Metrics: lo.Map(v.Metrics, decodeMetric),
		Logs: logsJSON{
			Selector:      v.Logs.Selector,
			ErrorPatterns: lo.Map(v.Logs.ErrorPatterns, decodeErrorPattern),
		},
		Runbooks:     lo.Map(v.Runbooks, decodeRunbook),
		Endpoints:    lo.Map(v.Endpoints, decodeEndpoint),
		Dependencies: lo.Map(v.Dependencies, decodeDependency),
		DocsUrl:      v.DocsUrl,
		Domain:       decodeDomain(v.Domain),
	}
}

func decodeDomain(v *domainModel.Domain) *domainJSON {
	if v == nil {
		return nil
	}
	return &domainJSON{
		Responsibilities: v.Responsibilities,
		NotResponsible: lo.Map(v.NotResponsible, func(b domainModel.Boundary, _ int) boundaryJSON {
			return boundaryJSON{What: b.What, Service: b.Service}
		}),
		Entities: lo.Map(v.Entities, func(e domainModel.Entity, _ int) entityJSON {
			return entityJSON{
				Name: e.Name, IdPattern: e.IdPattern, IdExample: e.IdExample, Description: e.Description,
				Statuses: lo.Map(e.Statuses, func(s domainModel.EntityStatus, _ int) entityStatusJSON {
					return entityStatusJSON{Name: s.Name, Meaning: s.Meaning, StuckAfter: int64(s.StuckAfter / time.Second)}
				}),
			}
		}),
		Questions: lo.Map(v.Questions, func(q domainModel.Question, _ int) questionJSON {
			return questionJSON{Question: q.Question, How: q.How, Endpoint: q.Endpoint}
		}),
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

func decodeDependency(v domainModel.Dependency, _ int) dependencyJSON {
	return dependencyJSON{Id: v.Id, Kind: v.Kind, Target: v.Target, Service: v.Service, Critical: v.Critical, Affects: v.Affects}
}

func decodeEndpoint(v domainModel.Endpoint, _ int) endpointJSON {
	result := endpointJSON{
		Id:          v.Id,
		Title:       v.Title,
		Description: v.Description,
		Path:        v.Path,
		Audience:    v.Audience,
		Params: lo.MapValues(v.Params, func(p domainModel.EndpointParam, _ string) endpointParamJSON {
			return endpointParamJSON{
				Type: p.Type, Default: p.Default, Max: p.Max, Min: p.Min, Required: p.Required,
				Pattern: p.Pattern, Enum: p.Enum, Description: p.Description, Personal: p.Personal,
			}
		}),
		Response: decodeSchema(v.Response),
		RowsPath: v.RowsPath,
		MaxRows:  v.MaxRows,
		Timeout:  v.Timeout.Milliseconds(),
	}
	if v.Workload != nil {
		result.Workload = &workloadRefJSON{Namespace: v.Workload.Namespace, Kind: v.Workload.Kind, Name: v.Workload.Name}
	}
	return result
}

func decodeSchema(v *domainModel.Schema) *schemaJSON {
	if v == nil {
		return nil
	}
	result := &schemaJSON{
		Type: v.Type, Items: decodeSchema(v.Items), Values: decodeSchema(v.Values), Enum: v.Enum, Format: v.Format,
		MaxLength: v.MaxLength, MaxItems: v.MaxItems, Description: v.Description, Personal: v.Personal,
	}
	if len(v.Properties) > 0 {
		result.Properties = lo.MapValues(v.Properties, func(p *domainModel.Schema, _ string) *schemaJSON { return decodeSchema(p) })
	}
	return result
}
