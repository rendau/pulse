package model

import (
	"fmt"
	"time"

	"github.com/samber/lo"
	"go.yaml.in/yaml/v3"

	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
)

// ServiceYaml — транспортная модель файла service.yaml.
// Содержит только то, что не выводится из кластера.
type ServiceYaml struct {
	Name    string   `yaml:"name"`
	Title   string   `yaml:"title"`
	Aliases []string `yaml:"aliases"`
	Owner   struct {
		Team     string   `yaml:"team"`
		Contacts []string `yaml:"contacts"`
	} `yaml:"owner"`
	Criticality string `yaml:"criticality"`
	Description string `yaml:"description"`

	Metrics []struct {
		Id        string `yaml:"id"`
		Title     string `yaml:"title"`
		PromQL    string `yaml:"promql"`
		Unit      string `yaml:"unit"`
		Direction string `yaml:"direction"`
	} `yaml:"metrics"`

	Logs struct {
		Selector      string `yaml:"selector"`
		ErrorPatterns []struct {
			Name    string `yaml:"name"`
			Pattern string `yaml:"pattern"`
		} `yaml:"error_patterns"`
	} `yaml:"logs"`

	Runbooks []struct {
		Title string `yaml:"title"`
		Url   string `yaml:"url"`
	} `yaml:"runbooks"`

	Endpoints []struct {
		Id     string `yaml:"id"`
		Title  string `yaml:"title"`
		Path   string `yaml:"path"`
		Method string `yaml:"method"`
		Port   int    `yaml:"port"`
		K8sSvc string `yaml:"k8s_service"`
		Params map[string]struct {
			Type     string   `yaml:"type"`
			Default  any      `yaml:"default"`
			Max      *float64 `yaml:"max"`
			Min      *float64 `yaml:"min"`
			Required bool     `yaml:"required"`
		} `yaml:"params"`
		MaxRows int      `yaml:"max_rows"`
		PII     []string `yaml:"pii"`
		Timeout string   `yaml:"timeout"`
	} `yaml:"endpoints"`
}

// ParseServiceYaml разбирает содержимое файла; name обязателен.
func ParseServiceYaml(raw []byte) (*ServiceYaml, error) {
	result := &ServiceYaml{}
	if err := yaml.Unmarshal(raw, result); err != nil {
		return nil, fmt.Errorf("yaml: %w", err)
	}
	if result.Name == "" {
		return nil, fmt.Errorf("service.yaml: name is required")
	}
	return result, nil
}

// DecodeServiceYaml переводит транспортную модель в правку каталога.
func DecodeServiceYaml(v *ServiceYaml) *svcModel.Edit {
	return &svcModel.Edit{
		Name:            new(v.Name),
		Title:           new(v.Title),
		Description:     new(v.Description),
		Criticality:     new(v.Criticality),
		OwnerTeam:       new(v.Owner.Team),
		OwnerContacts:   new(lo.Compact(v.Owner.Contacts)),
		Aliases:         new(lo.Compact(v.Aliases)),
		MetadataPresent: new(true),
		Metadata:        new(decodeMetadata(v)),
	}
}

func decodeMetadata(v *ServiceYaml) svcModel.Metadata {
	result := svcModel.Metadata{
		Source: svcModel.MetadataSourceServiceYaml,
		Logs:   svcModel.Logs{Selector: v.Logs.Selector},
	}

	for _, m := range v.Metrics {
		result.Metrics = append(result.Metrics, svcModel.Metric{
			Id: m.Id, Title: m.Title, PromQL: m.PromQL, Unit: m.Unit, Direction: m.Direction,
		})
	}
	for _, p := range v.Logs.ErrorPatterns {
		result.Logs.ErrorPatterns = append(result.Logs.ErrorPatterns, svcModel.ErrorPattern{Name: p.Name, Pattern: p.Pattern})
	}
	for _, r := range v.Runbooks {
		result.Runbooks = append(result.Runbooks, svcModel.Runbook{Title: r.Title, Url: r.Url})
	}
	for _, e := range v.Endpoints {
		endpoint := svcModel.Endpoint{
			Id:         e.Id,
			Title:      e.Title,
			Path:       e.Path,
			Method:     e.Method,
			Port:       e.Port,
			K8sService: e.K8sSvc,
			MaxRows:    e.MaxRows,
			PII:        e.PII,
			Params:     make(map[string]svcModel.EndpointParam, len(e.Params)),
		}
		if e.Timeout != "" {
			endpoint.Timeout, _ = time.ParseDuration(e.Timeout)
		}
		for name, p := range e.Params {
			param := svcModel.EndpointParam{Type: p.Type, Max: p.Max, Min: p.Min, Required: p.Required}
			if p.Default != nil {
				param.Default = fmt.Sprint(p.Default)
			}
			endpoint.Params[name] = param
		}
		result.Endpoints = append(result.Endpoints, endpoint)
	}

	return result
}
