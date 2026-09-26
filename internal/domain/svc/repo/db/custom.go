package db

import (
	"github.com/rendau/pulse/internal/domain/svc/model"
)

var allowedSortFields = map[string]string{
	"name":       "name",
	"owner_team": "owner_team",
	"last_seen":  "last_seen",
}

func (r *Repo) getConditions(pars *model.ListReq) (map[string]any, map[string][]any) {
	conditions := make(map[string]any, 6)
	conditionExps := make(map[string][]any, 4)

	if pars == nil {
		return conditions, conditionExps
	}

	if len(pars.Names) > 0 {
		conditions["name"] = pars.Names
	}
	if pars.Team != nil {
		conditions["owner_team"] = *pars.Team
	}
	if pars.Criticality != nil {
		conditions["criticality"] = *pars.Criticality
	}
	if pars.HasMetadata != nil {
		conditions["metadata_present"] = *pars.HasMetadata
	}
	if pars.RepoUrl != nil {
		conditions["repo_url"] = *pars.RepoUrl
	}
	if pars.Namespace != nil {
		conditionExps["name in (select service_name from workload where namespace = ?)"] = []any{*pars.Namespace}
	}
	if pars.Search != nil && *pars.Search != "" {
		pattern := "%" + *pars.Search + "%"
		conditionExps["(name ilike ? or title ilike ? or array_to_string(aliases, ' ') ilike ? or array_to_string(cluster_names, ' ') ilike ?)"] = []any{pattern, pattern, pattern, pattern}
	}

	return conditions, conditionExps
}
