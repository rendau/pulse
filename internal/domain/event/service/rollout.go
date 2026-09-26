package service

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/rendau/pulse/internal/constant"
	"github.com/rendau/pulse/internal/domain/event/model"
)

const (
	// reloaderAnnotation — JSON {type, name, namespace, hash, …} объекта, после смены которого
	// reloader перекатил поды
	reloaderAnnotation = "reloader.stakater.com/last-reloaded-from"
	restartAnnotation  = "kubectl.kubernetes.io/restartedAt"
	// hashLen — длина отпечатка в ответе: достаточно, чтобы отличить версии
	hashLen = 12
)

type reloadedFrom struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Hash string `json:"hash"`
}

// Rollouts выводит причины выкаток за окно из соседних ревизий шаблона пода: изменилась
// аннотация reloader — выкатка из-за смены configmap/secret, изменилась restartedAt — ручной
// рестарт. Ревизия без предыдущей (удалена по revisionHistoryLimit) не сравнивается.
func (s *Service) Rollouts(workload string, revisions []model.PodTemplateRevision, since time.Time) []model.Rollout {
	sorted := append([]model.PodTemplateRevision(nil), revisions...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Revision < sorted[j].Revision })

	result := make([]model.Rollout, 0, 2)
	for i := 1; i < len(sorted); i++ {
		prev, cur := sorted[i-1], sorted[i]
		if cur.CreatedAt.Before(since) {
			continue
		}

		curReload, curOk := parseReloadedFrom(cur.Annotations[reloaderAnnotation])
		prevReload, _ := parseReloadedFrom(prev.Annotations[reloaderAnnotation])
		if curOk && (curReload.Hash != prevReload.Hash || curReload.Name != prevReload.Name) {
			result = append(result, model.Rollout{
				TS: cur.CreatedAt, Workload: workload, Revision: cur.Revision, Cause: model.RolloutCauseConfigReload,
				ConfigKind: strings.ToLower(curReload.Type), ConfigName: curReload.Name,
				Hash: shortHash(curReload.Hash), PrevHash: shortHash(prevReload.Hash),
			})
			continue
		}

		if restartedAt := cur.Annotations[restartAnnotation]; restartedAt != "" && restartedAt != prev.Annotations[restartAnnotation] {
			result = append(result, model.Rollout{TS: cur.CreatedAt, Workload: workload, Revision: cur.Revision, Cause: model.RolloutCauseRestart})
		}
	}
	return result
}

// FromRollout — событие выкатки с причиной.
func (s *Service) FromRollout(r model.Rollout, service string) model.Event {
	if r.Cause == model.RolloutCauseRestart {
		return model.Event{
			TS: r.TS, Source: constant.SourceK8s, Type: constant.EventTypeRestart, Service: service, Severity: constant.SeverityInfo,
			Summary: fmt.Sprintf("%s: ручной рестарт %s (rollout restart, ревизия %d)", service, r.Workload, r.Revision),
			Details: map[string]any{"workload": r.Workload, "revision": r.Revision},
		}
	}
	summary := fmt.Sprintf("%s: изменён %s %s, поды %s перекачены (reloader, ревизия %d)", service, r.ConfigKind, r.ConfigName, r.Workload, r.Revision)
	details := map[string]any{
		"workload": r.Workload, "revision": r.Revision, "kind": r.ConfigKind, "name": r.ConfigName,
		"hash": r.Hash, "prev_hash": r.PrevHash,
	}
	if r.Sync != nil {
		keys := lo.FlatMap(lo.Filter(r.Sync.Objects, func(o model.ConfigSyncObject, _ int) bool { return o.ObjectName == r.ConfigName }),
			func(o model.ConfigSyncObject, _ int) []string { return o.ChangedKeys })
		summary = fmt.Sprintf("%s: kusec применил %s %s%s%s, поды %s перекачены (ревизия %d)",
			service, r.ConfigKind, r.ConfigName, keysSuffix(keys), authorSuffix(r.Sync.Author), r.Workload, r.Revision)
		details["sync_run_id"], details["author"], details["changed_keys"] = r.Sync.RunId, r.Sync.Author, keys
	}
	return model.Event{
		TS: r.TS, Source: constant.SourceK8s, Type: constant.EventTypeConfigChange, Service: service, Severity: constant.SeverityInfo,
		Summary: summary, Details: details,
	}
}

func parseReloadedFrom(raw string) (reloadedFrom, bool) {
	if raw == "" {
		return reloadedFrom{}, false
	}
	v := reloadedFrom{}
	if err := json.Unmarshal([]byte(raw), &v); err != nil || v.Name == "" {
		return reloadedFrom{}, false
	}
	return v, true
}

func shortHash(hash string) string {
	if len(hash) > hashLen {
		return hash[:hashLen]
	}
	return hash
}
