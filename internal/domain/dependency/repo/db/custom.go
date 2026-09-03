package db

import (
	"github.com/mechta-market/pulse/internal/domain/dependency/model"
)

var allowedSortFields = map[string]string{
	"from_service": "from_service",
	"to_host":      "to_host",
	"last_seen":    "last_seen",
}

func (r *Repo) getConditions(pars *model.ListReq) (map[string]any, map[string][]any) {
	conditions := make(map[string]any, 3)
	conditionExps := make(map[string][]any, 1)

	if pars == nil {
		return conditions, conditionExps
	}

	if pars.Cluster != nil {
		conditions["cluster"] = *pars.Cluster
	}
	if len(pars.FromServices) > 0 {
		conditions["from_service"] = pars.FromServices
	}
	if len(pars.ToServices) > 0 {
		conditions["to_service"] = pars.ToServices
	}

	return conditions, conditionExps
}
