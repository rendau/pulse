package model

import (
	"fmt"

	"github.com/samber/lo"
	"go.yaml.in/yaml/v3"

	svcModel "github.com/rendau/pulse/internal/domain/svc/model"
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

	return result
}
