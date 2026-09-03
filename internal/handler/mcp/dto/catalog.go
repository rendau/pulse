package dto

import (
	"time"

	"github.com/samber/lo"

	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	catalogModel "github.com/mechta-market/pulse/internal/usecase/catalog/model"
)

// resolve_service

type ResolveServiceReq struct {
	Query string `json:"query" jsonschema:"человеческая формулировка: имя, алиас, слово из описания (например «платежи», «kafka producer»)"`
}

type ResolveServiceRep struct {
	Candidates []Candidate `json:"candidates"`
	Ambiguous  bool        `json:"ambiguous" jsonschema:"true — уверенность лидера ниже 0.5, переспроси пользователя, показав кандидатов"`
}

type Candidate struct {
	Service    string   `json:"service"`
	Title      string   `json:"title,omitempty"`
	Confidence float64  `json:"confidence"`
	MatchedBy  string   `json:"matched_by" jsonschema:"name | alias | title | fuzzy | description"`
	Namespaces []string `json:"namespaces,omitempty"`
	OwnerTeam  string   `json:"owner_team,omitempty"`
}

func EncodeResolveServiceRep(v *catalogModel.ResolveResult) ResolveServiceRep {
	return ResolveServiceRep{
		Ambiguous:  v.Ambiguous,
		Candidates: lo.Map(v.Candidates, encodeCandidate),
	}
}

func encodeCandidate(v *catalogModel.Candidate, _ int) Candidate {
	return Candidate{
		Service:    v.Service.Name,
		Title:      v.Service.Title,
		Confidence: v.Confidence,
		MatchedBy:  v.MatchedBy,
		Namespaces: v.Namespaces,
		OwnerTeam:  v.Service.OwnerTeam,
	}
}

// list_services

type ListServicesReq struct {
	Team        *string `json:"team,omitempty" jsonschema:"команда-владелец из service.yaml"`
	Namespace   *string `json:"namespace,omitempty" jsonschema:"namespace кластера"`
	Criticality *string `json:"criticality,omitempty" jsonschema:"high | medium | low"`
	HasMetadata *bool   `json:"has_metadata,omitempty" jsonschema:"только сервисы с service.yaml (true) или без него (false)"`
	Search      *string `json:"search,omitempty" jsonschema:"подстрока в имени, title или алиасах"`
	Page        int64   `json:"page,omitempty" jsonschema:"номер страницы, с 0"`
	PageSize    int64   `json:"page_size,omitempty" jsonschema:"размер страницы, по умолчанию 100, максимум 500"`
}

func DecodeListServicesReq(v ListServicesReq) *catalogModel.ListReq {
	return &catalogModel.ListReq{
		Team:        v.Team,
		Namespace:   v.Namespace,
		Criticality: v.Criticality,
		HasMetadata: v.HasMetadata,
		Search:      v.Search,
		Page:        v.Page,
		PageSize:    v.PageSize,
	}
}

type ListServicesRep struct {
	Services   []ServiceSummary `json:"services"`
	TotalCount int64            `json:"total_count"`
	Truncated  bool             `json:"truncated" jsonschema:"true — есть ещё страницы"`
}

type ServiceSummary struct {
	Service     string   `json:"service"`
	Title       string   `json:"title,omitempty"`
	OwnerTeam   string   `json:"owner_team,omitempty"`
	Criticality string   `json:"criticality,omitempty"`
	Namespaces  []string `json:"namespaces,omitempty"`
	HasMetadata bool     `json:"has_metadata"`
	// External — сторонний образ (postgres, redis…) без репозитория компании
	External bool `json:"external,omitempty"`
}

func EncodeListServicesRep(items []*catalogModel.ServiceSummary, total int64, page, pageSize int64) ListServicesRep {
	return ListServicesRep{
		Services:   lo.Map(items, encodeServiceSummary),
		TotalCount: total,
		Truncated:  (page+1)*pageSize < total,
	}
}

func encodeServiceSummary(v *catalogModel.ServiceSummary, _ int) ServiceSummary {
	return ServiceSummary{
		Service:     v.Service.Name,
		Title:       v.Service.Title,
		OwnerTeam:   v.Service.OwnerTeam,
		Criticality: v.Service.Criticality,
		Namespaces:  v.Namespaces,
		HasMetadata: v.Service.MetadataPresent,
		External:    v.Service.RepoUrl == "",
	}
}

// get_service_info

type GetServiceInfoReq struct {
	Service string `json:"service" jsonschema:"точное имя сервиса из resolve_service или list_services"`
}

type ServiceInfoRep struct {
	Service       string         `json:"service"`
	Title         string         `json:"title,omitempty"`
	Description   string         `json:"description,omitempty"`
	Criticality   string         `json:"criticality,omitempty"`
	OwnerTeam     string         `json:"owner_team,omitempty"`
	OwnerContacts []string       `json:"owner_contacts,omitempty"`
	Aliases       []string       `json:"aliases,omitempty"`
	RepoUrl       string         `json:"repo_url,omitempty"`
	HasMetadata   bool           `json:"has_metadata"`
	Metrics       []MetricDef    `json:"metrics,omitempty"`
	LogsSelector  string         `json:"logs_selector,omitempty"`
	Runbooks      []Runbook      `json:"runbooks,omitempty"`
	Endpoints     []EndpointDef  `json:"diagnostic_endpoints,omitempty" jsonschema:"диагностические ручки из service.yaml; вызов — call_service_endpoint"`
	Workloads     []WorkloadInfo `json:"workloads"`
	FirstSeen     time.Time      `json:"first_seen"`
	LastSeen      time.Time      `json:"last_seen"`
	Errors        []SourceError  `json:"errors,omitempty" jsonschema:"источники, которые не ответили: часть картины отсутствует"`
}

type MetricDef struct {
	Id        string `json:"id"`
	Title     string `json:"title,omitempty"`
	Unit      string `json:"unit,omitempty"`
	Direction string `json:"direction,omitempty"`
}

type Runbook struct {
	Title string `json:"title"`
	Url   string `json:"url"`
}

type WorkloadInfo struct {
	Cluster         string     `json:"cluster"`
	Namespace       string     `json:"namespace"`
	Kind            string     `json:"kind"`
	Name            string     `json:"name"`
	ReplicasDesired int32      `json:"replicas_desired"`
	Image           string     `json:"image"`
	ImageDigest     string     `json:"image_digest,omitempty"`
	DeployedCommit  string     `json:"deployed_commit,omitempty"`
	Pods            *PodsState `json:"pods,omitempty" jsonschema:"живое состояние подов; отсутствует, если кластер не ответил или у workload нет подов (CronJob)"`
}

type PodsState struct {
	Ready    int      `json:"ready"`
	Total    int      `json:"total"`
	Restarts int32    `json:"restarts"`
	Problems []string `json:"problems,omitempty"`
}

type SourceError struct {
	Source  string `json:"source"`
	Message string `json:"message"`
}

func EncodeServiceInfoRep(v *catalogModel.ServiceInfo) ServiceInfoRep {
	return ServiceInfoRep{
		Service:       v.Service.Name,
		Title:         v.Service.Title,
		Description:   v.Service.Description,
		Criticality:   v.Service.Criticality,
		OwnerTeam:     v.Service.OwnerTeam,
		OwnerContacts: v.Service.OwnerContacts,
		Aliases:       v.Service.Aliases,
		RepoUrl:       v.Service.RepoUrl,
		HasMetadata:   v.Service.MetadataPresent,
		Metrics:       lo.Map(v.Service.Metadata.Metrics, encodeMetricDef),
		LogsSelector:  v.Service.Metadata.Logs.Selector,
		Runbooks:      lo.Map(v.Service.Metadata.Runbooks, encodeRunbook),
		Endpoints:     lo.Map(v.Service.Metadata.Endpoints, EncodeEndpointDef),
		Workloads:     lo.Map(v.Workloads, encodeWorkloadInfo),
		FirstSeen:     v.Service.FirstSeen.UTC(),
		LastSeen:      v.Service.LastSeen.UTC(),
		Errors:        lo.Map(v.Errors, encodeSourceError),
	}
}

func encodeMetricDef(v svcModel.Metric, _ int) MetricDef {
	return MetricDef{Id: v.Id, Title: v.Title, Unit: v.Unit, Direction: v.Direction}
}

func encodeRunbook(v svcModel.Runbook, _ int) Runbook {
	return Runbook{Title: v.Title, Url: v.Url}
}

func encodeWorkloadInfo(v *catalogModel.WorkloadInfo, _ int) WorkloadInfo {
	return WorkloadInfo{
		Cluster:         v.Workload.Cluster,
		Namespace:       v.Workload.Namespace,
		Kind:            v.Workload.Kind,
		Name:            v.Workload.Name,
		ReplicasDesired: v.Workload.ReplicasDesired,
		Image:           v.Workload.Image,
		ImageDigest:     v.Workload.ImageDigest,
		DeployedCommit:  v.Workload.DeployedCommit,
		Pods:            encodePodsState(v.Pods),
	}
}

func encodePodsState(v *catalogModel.PodsState) *PodsState {
	if v == nil {
		return nil
	}
	return &PodsState{Ready: v.Ready, Total: v.Total, Restarts: v.Restarts, Problems: v.Problems}
}

func encodeSourceError(v catalogModel.SourceError, _ int) SourceError {
	return SourceError{Source: v.Source, Message: v.Message}
}
