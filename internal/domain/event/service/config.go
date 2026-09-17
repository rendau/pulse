package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/mechta-market/pulse/internal/constant"
	"github.com/mechta-market/pulse/internal/domain/event/model"
)

const (
	// syncBeforeRollout — насколько раньше выкатки reloader'а мог начаться применивший её sync
	syncBeforeRollout = 15 * time.Minute
	// syncAfterRollout — допуск на расхождение часов kusec и кластера
	syncAfterRollout = time.Minute
	// maxKeysInSummary — сколько имён ключей перечислять в тексте события
	maxKeysInSummary = 5
)

var actionTitles = map[string]string{
	"create": "добавлен", "update": "изменён", "delete": "удалён",
	"activate": "включён", "deactivate": "выключен", "import": "импортирован",
}

// FromConfigEdit — событие правки конфигурации в kusec. Правка ещё не в кластере: применяет её sync.
func (s *Service) FromConfigEdit(e model.ConfigEdit, service string) model.Event {
	action := lo.CoalesceOrEmpty(actionTitles[e.Action], e.Action)
	subject := fmt.Sprintf("ключ %s в %s %s", e.Key, e.ObjectKind, e.ObjectName)
	if e.Key == "" {
		subject = fmt.Sprintf("%s %s", e.ObjectKind, e.ObjectName)
	}

	summary := fmt.Sprintf("%s: в kusec %s %s", service, action, subject)
	if e.ValueChanged && e.Action == "update" {
		summary += fmt.Sprintf(" (%s → %s)", e.OldValue, e.NewValue)
	}
	if len(e.Fields) > 0 {
		summary += ", поля: " + strings.Join(e.Fields, ", ")
	}
	summary += authorSuffix(e.Author)

	return model.Event{
		TS: e.TS, Source: constant.SourceKusec, Type: constant.EventTypeConfigChange, Service: service, Severity: constant.SeverityInfo,
		Summary: summary,
		Details: map[string]any{
			"action": e.Action, "kind": e.ObjectKind, "name": e.ObjectName, "key": e.Key, "author": e.Author, "origin": e.Origin,
			"value_changed": e.ValueChanged, "old": e.OldValue, "new": e.NewValue, "fields": e.Fields,
		},
	}
}

// FromConfigSync — применение конфигурации в кластер, не связанное с выкаткой (reloader не
// перекатил поды: объект не подключён к подам с аннотацией reloader либо выкатка вне окна).
func (s *Service) FromConfigSync(sync model.ConfigSync, service string) model.Event {
	objects := lo.Map(sync.Objects, func(o model.ConfigSyncObject, _ int) string {
		return fmt.Sprintf("%s %s %s%s", o.ObjectKind, o.ObjectName, o.Op, keysSuffix(o.ChangedKeys))
	})
	severity := constant.SeverityInfo
	summary := fmt.Sprintf("%s: kusec применил конфигурацию в кластер: %s%s", service, strings.Join(objects, "; "), authorSuffix(sync.Author))
	if sync.Status != "" && sync.Status != "ok" {
		severity = constant.SeverityWarning
		summary += fmt.Sprintf(" — статус %s", sync.Status)
		if sync.Error != "" {
			summary += ": " + sync.Error
		}
	}
	return model.Event{
		TS: sync.TS, Source: constant.SourceKusec, Type: constant.EventTypeConfigChange, Service: service, Severity: severity,
		Summary: summary,
		Details: map[string]any{"sync_run_id": sync.RunId, "status": sync.Status, "author": sync.Author, "objects": sync.Objects},
	}
}

// LinkSyncs связывает выкатки reloader'а с запусками sync kusec: объект выкатки изменён sync'ом,
// начавшимся не раньше чем за syncBeforeRollout до неё (ближайший). Возвращает выкатки с Sync и
// запуски, которые ни к одной выкатке не привязались, — у них отдельное событие.
func (s *Service) LinkSyncs(rollouts []model.Rollout, syncs []model.ConfigSync) ([]model.Rollout, []model.ConfigSync) {
	linked := make(map[string]struct{}, len(syncs))
	result := make([]model.Rollout, len(rollouts))

	for i, r := range rollouts {
		result[i] = r
		if r.Cause != model.RolloutCauseConfigReload {
			continue
		}
		var best *model.ConfigSync
		for j := range syncs {
			candidate := &syncs[j]
			if candidate.TS.Before(r.TS.Add(-syncBeforeRollout)) || candidate.TS.After(r.TS.Add(syncAfterRollout)) {
				continue
			}
			if !lo.ContainsBy(candidate.Objects, func(o model.ConfigSyncObject) bool {
				return o.ObjectName == r.ConfigName && o.ObjectKind == r.ConfigKind
			}) {
				continue
			}
			if best == nil || r.TS.Sub(candidate.TS).Abs() < r.TS.Sub(best.TS).Abs() {
				best = candidate
			}
		}
		if best != nil {
			result[i].Sync = best
			linked[best.RunId] = struct{}{}
		}
	}

	unlinked := lo.Filter(syncs, func(v model.ConfigSync, _ int) bool {
		_, ok := linked[v.RunId]
		return !ok
	})
	return result, unlinked
}

func authorSuffix(author string) string {
	if author == "" {
		return ""
	}
	return ", автор " + author
}

func keysSuffix(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	if len(keys) > maxKeysInSummary {
		return fmt.Sprintf(" (%s и ещё %d)", strings.Join(keys[:maxKeysInSummary], ", "), len(keys)-maxKeysInSummary)
	}
	return " (" + strings.Join(keys, ", ") + ")"
}
