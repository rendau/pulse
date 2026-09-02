package model

import (
	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
)

// ResolveResult — кандидаты на формулировку пользователя.
type ResolveResult struct {
	Candidates []*Candidate
	// Ambiguous — уверенность лидера ниже порога: агент должен переспросить
	Ambiguous bool
}

type Candidate struct {
	Service    *svcModel.Main
	Confidence float64
	MatchedBy  string
	Namespaces []string
}

// ListReq — фильтры обзорного списка.
type ListReq struct {
	Team        *string
	Namespace   *string
	Criticality *string
	HasMetadata *bool
	Search      *string
	Page        int64
	PageSize    int64
}

// ServiceSummary — компактная строка списка.
type ServiceSummary struct {
	Service    *svcModel.Main
	Namespaces []string
}

// ServiceInfo — карточка сервиса: метаданные, workloads и живое состояние подов.
type ServiceInfo struct {
	Service   *svcModel.Main
	Workloads []*WorkloadInfo
	Errors    []SourceError
}

type WorkloadInfo struct {
	Workload *workloadModel.Main
	Pods     *PodsState
}

// PodsState — состояние подов workload'а, запрошенное живьём.
type PodsState struct {
	Ready    int
	Total    int
	Restarts int32
	Problems []string
}

// SourceError — источник не ответил; часть картины отсутствует.
type SourceError struct {
	Source  string
	Message string
}
