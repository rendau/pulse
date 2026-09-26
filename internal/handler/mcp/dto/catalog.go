package dto

import (
	"time"

	"github.com/samber/lo"

	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	catalogModel "github.com/mechta-market/pulse/internal/usecase/catalog/model"
	"github.com/mechta-market/pulse/internal/util/tz"
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
	MatchedBy  string   `json:"matched_by" jsonschema:"name | alias | cluster_name | title | fuzzy | translit (латинский вариант кириллицы — догадка) | description"`
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
	Team        *string `json:"team,omitempty" jsonschema:"команда-владелец (из манифеста сервиса или service.yaml)"`
	Namespace   *string `json:"namespace,omitempty" jsonschema:"namespace кластера"`
	Criticality *string `json:"criticality,omitempty" jsonschema:"high | medium | low"`
	HasMetadata *bool   `json:"has_metadata,omitempty" jsonschema:"только сервисы с метаданными — манифест или service.yaml (true) — или без них (false)"`
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
	// Manifest — манифест сервиса (лучший статус среди workload'ов)
	Manifest string `json:"manifest,omitempty" jsonschema:"манифест сервиса: ok | partial | invalid | absent | unreachable; пусто — ещё не искали"`
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
		Manifest:    v.Manifest,
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
	ClusterNames  []string       `json:"cluster_names,omitempty" jsonschema:"имена сервиса в кластере: workload'ы, k8s Service, приложения ruto"`
	RepoUrl       string         `json:"repo_url,omitempty"`
	HasMetadata   bool           `json:"has_metadata"`
	MetadataFrom  string         `json:"metadata_source,omitempty" jsonschema:"откуда метаданные: manifest (сервис рассказал сам) | service_yaml"`
	DocsUrl       string         `json:"docs_url,omitempty"`
	Dependencies  []Dependency   `json:"dependencies,omitempty" jsonschema:"зависимости, которые сервис объявил сам (манифест)"`
	Metrics       []MetricDef    `json:"metrics,omitempty"`
	LogsSelector  string         `json:"logs_selector,omitempty"`
	Runbooks      []Runbook      `json:"runbooks,omitempty"`
	Endpoints     []EndpointDef  `json:"diagnostic_endpoints,omitempty" jsonschema:"диагностические ручки сервиса; вызов — call_service_endpoint"`
	Workloads     []WorkloadInfo `json:"workloads"`
	FirstSeen     time.Time      `json:"first_seen"`
	LastSeen      time.Time      `json:"last_seen"`
	Errors        []SourceError  `json:"errors,omitempty" jsonschema:"источники, которые не ответили: часть картины отсутствует"`
}

type Dependency struct {
	Id       string `json:"id"`
	Kind     string `json:"kind"`
	Target   string `json:"target"`
	Critical bool   `json:"critical,omitempty" jsonschema:"без неё сервис не работает"`
	Affects  string `json:"affects,omitempty" jsonschema:"что ломается, когда она недоступна (со слов владельца)"`
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
	Manifest        *Manifest  `json:"manifest,omitempty" jsonschema:"поиск манифеста сервиса на подах (docs/service-manifest.md)"`
}

type Manifest struct {
	Status    string    `json:"status" jsonschema:"ok | partial (часть отклонена) | invalid | absent (порт отвечает, манифеста нет) | unreachable"`
	Reasons   []string  `json:"reasons,omitempty" jsonschema:"что отклонено или почему не принят"`
	Port      int       `json:"port,omitempty"`
	Tried     []string  `json:"tried,omitempty" jsonschema:"какие порты пробовали и что там было"`
	CheckedAt time.Time `json:"checked_at"`
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
		ClusterNames:  v.Service.ClusterNames,
		RepoUrl:       v.Service.RepoUrl,
		HasMetadata:   v.Service.MetadataPresent,
		MetadataFrom:  v.Service.Metadata.Source,
		DocsUrl:       v.Service.Metadata.DocsUrl,
		Dependencies:  lo.Map(v.Service.Metadata.Dependencies, encodeDependency),
		Metrics:       lo.Map(v.Service.Metadata.Metrics, encodeMetricDef),
		LogsSelector:  v.Service.Metadata.Logs.Selector,
		Runbooks:      lo.Map(v.Service.Metadata.Runbooks, encodeRunbook),
		Endpoints:     lo.Map(v.Service.Metadata.Endpoints, EncodeEndpointDef),
		Workloads:     lo.Map(v.Workloads, encodeWorkloadInfo),
		FirstSeen:     tz.In(v.Service.FirstSeen),
		LastSeen:      tz.In(v.Service.LastSeen),
		Errors:        lo.Map(v.Errors, encodeSourceError),
	}
}

func encodeDependency(v svcModel.Dependency, _ int) Dependency {
	return Dependency{Id: v.Id, Kind: v.Kind, Target: v.Target, Critical: v.Critical, Affects: v.Affects}
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
		Manifest:        encodeManifest(v.Workload.Manifest),
	}
}

func encodeManifest(v workloadModel.Manifest) *Manifest {
	if v.Status == "" {
		return nil
	}
	return &Manifest{Status: v.Status, Reasons: v.Reasons, Port: v.Port, Tried: v.Tried, CheckedAt: tz.In(v.CheckedAt)}
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
