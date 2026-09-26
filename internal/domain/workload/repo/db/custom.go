package db

import (
	"github.com/rendau/pulse/internal/domain/workload/model"
)

var allowedSortFields = map[string]string{
	"namespace": "namespace",
	"kind":      "kind",
	"name":      "name",
	"last_seen": "last_seen",
}

func (r *Repo) getConditions(pars *model.ListReq) (map[string]any, map[string][]any) {
	conditions := make(map[string]any, 5)
	conditionExps := make(map[string][]any, 1)

	if pars == nil {
		return conditions, conditionExps
	}

	if pars.Cluster != nil {
		conditions["cluster"] = *pars.Cluster
	}
	if pars.Namespace != nil {
		conditions["namespace"] = *pars.Namespace
	}
	if pars.Kind != nil {
		conditions["kind"] = *pars.Kind
	}
	if pars.ServiceName != nil {
		conditions["service_name"] = *pars.ServiceName
	}
	if len(pars.ServiceNames) > 0 {
		conditions["service_name"] = pars.ServiceNames
	}

	return conditions, conditionExps
}
