package db

import (
	"github.com/mechta-market/pulse/internal/domain/deploy/model"
)

var allowedSortFields = map[string]string{
	"observed_at": "observed_at",
	"id":          "id",
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
	if len(pars.ServiceNames) > 0 {
		conditions["service_name"] = pars.ServiceNames
	}
	if pars.Since != nil {
		conditionExps["observed_at >= ?"] = []any{*pars.Since}
	}

	return conditions, conditionExps
}
