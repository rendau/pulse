package timeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	"github.com/mechta-market/pulse/internal/constant"
	eventModel "github.com/mechta-market/pulse/internal/domain/event/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	"github.com/mechta-market/pulse/internal/errs"
	kusecModel "github.com/mechta-market/pulse/internal/service/kusec/model"
	"github.com/mechta-market/pulse/internal/usecase/timeline/model"
	"github.com/mechta-market/pulse/internal/util/redact"
)

// maxSyncRuns — запусков sync на приложение за окно (каждый — отдельный запрос за объектами)
const maxSyncRuns = 20

// configuration — всё об изменениях конфигурации сервисов за окно: выкатки reloader'а из кластера
// и, если подключён kusec, правки (аудит), запуски sync и расхождение с кластером. Выкатка,
// вызванная sync'ом, склеивается с ним в одно изменение.
type configuration struct {
	rollouts []serviceRollout
	edits    []serviceEdit
	// syncs — запуски sync, не привязавшиеся ни к одной выкатке
	syncs    []serviceSync
	unsynced []model.UnsyncedConfig
}

type serviceEdit struct {
	eventModel.ConfigEdit
	service string
}

type serviceSync struct {
	eventModel.ConfigSync
	service string
}

// kusecApp — приложение kusec и k8s-объекты сервиса, которые оно описывает.
type kusecApp struct {
	id      string
	service string
	objects map[string]struct{}
}

func (u *Usecase) configuration(ctx context.Context, workloads []*workloadModel.Main, since, until time.Time, withKusec, withDrift bool, addError func(string, error)) *configuration {
	result := &configuration{}
	var syncs []serviceSync

	eg, egCtx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		result.rollouts = u.rollouts(egCtx, workloads, since, addError)
		return nil
	})
	if withKusec {
		eg.Go(func() error {
			if u.kusec == nil {
				addError(constant.SourceKusec, errs.Err("not configured"))
				return nil
			}
			var err error
			if result.edits, syncs, result.unsynced, err = u.kusecHistory(egCtx, workloads, since, until, withDrift); err != nil {
				addError(constant.SourceKusec, err)
			}
			return nil
		})
	}
	_ = eg.Wait()

	// склейка по сервису: выкатка reloader'а ← sync kusec того же объекта
	services := lo.Uniq(append(
		lo.Map(result.rollouts, func(r serviceRollout, _ int) string { return r.service }),
		lo.Map(syncs, func(s serviceSync, _ int) string { return s.service })...,
	))
	rollouts := make([]serviceRollout, 0, len(result.rollouts))
	for _, service := range services {
		own := lo.FilterMap(result.rollouts, func(r serviceRollout, _ int) (eventModel.Rollout, bool) { return r.Rollout, r.service == service })
		ownSyncs := lo.FilterMap(syncs, func(s serviceSync, _ int) (eventModel.ConfigSync, bool) { return s.ConfigSync, s.service == service })
		linked, unlinked := u.events.LinkSyncs(own, ownSyncs)
		rollouts = append(rollouts, lo.Map(linked, func(r eventModel.Rollout, _ int) serviceRollout { return serviceRollout{Rollout: r, service: service} })...)
		result.syncs = append(result.syncs, lo.Map(unlinked, func(s eventModel.ConfigSync, _ int) serviceSync { return serviceSync{ConfigSync: s, service: service} })...)
	}
	result.rollouts = rollouts

	return result
}

// events — нормализованные события изменений конфигурации.
func (c *configuration) events(events eventServiceI) []eventModel.Event {
	result := make([]eventModel.Event, 0, len(c.rollouts)+len(c.edits)+len(c.syncs))
	for _, r := range c.rollouts {
		result = append(result, events.FromRollout(r.Rollout, r.service))
	}
	for _, e := range c.edits {
		result = append(result, events.FromConfigEdit(e.ConfigEdit, e.service))
	}
	for _, s := range c.syncs {
		result = append(result, events.FromConfigSync(s.ConfigSync, s.service))
	}
	return result
}

// changes — те же факты в виде списка изменений get_changes.
func (c *configuration) changes() []model.ConfigChange {
	result := make([]model.ConfigChange, 0, len(c.rollouts)+len(c.edits)+len(c.syncs))
	for _, e := range c.edits {
		result = append(result, model.ConfigChange{
			TS: e.TS, Source: model.ConfigChangeSourceKusec, Action: e.Action, Kind: e.ObjectKind, Object: e.ObjectName,
			Key: e.Key, OldValue: e.OldValue, NewValue: e.NewValue, Fields: e.Fields, Author: e.Author,
		})
	}
	for _, s := range c.syncs {
		for _, o := range s.Objects {
			result = append(result, model.ConfigChange{
				TS: s.TS, Source: model.ConfigChangeSourceKusecSync, Action: o.Op, Kind: o.ObjectKind, Object: o.ObjectName,
				ChangedKeys: o.ChangedKeys, Author: s.Author, SyncRunId: s.RunId, Status: lo.Ternary(s.Status == kusecModel.SyncStatusOk, "", s.Status),
			})
		}
	}
	for _, r := range c.rollouts {
		if r.Cause != eventModel.RolloutCauseConfigReload {
			continue
		}
		change := model.ConfigChange{
			TS: r.TS, Source: model.ConfigChangeSourceReloader, Action: "rollout", Kind: r.ConfigKind, Object: r.ConfigName,
			OldValue: r.PrevHash, NewValue: r.Hash, Workload: r.Workload,
		}
		// отпечаток содержимого секрета тоже не отдаётся (Р7)
		if r.ConfigKind == "secret" {
			change.OldValue, change.NewValue = redact.Secret(), redact.Secret()
		}
		if r.Sync != nil {
			change.Author, change.SyncRunId = r.Sync.Author, r.Sync.RunId
			change.ChangedKeys = lo.FlatMap(lo.Filter(r.Sync.Objects, func(o eventModel.ConfigSyncObject, _ int) bool { return o.ObjectName == r.ConfigName }),
				func(o eventModel.ConfigSyncObject, _ int) []string { return o.ChangedKeys })
		}
		result = append(result, change)
	}
	return result
}

// kusecHistory — правки, запуски sync и (withDrift) расхождения по приложениям kusec сервисов.
func (u *Usecase) kusecHistory(ctx context.Context, workloads []*workloadModel.Main, since, until time.Time, withDrift bool) (
	[]serviceEdit, []serviceSync, []model.UnsyncedConfig, error,
) {
	apps, err := u.kusecApps(ctx, workloads)
	if err != nil {
		return nil, nil, nil, err
	}

	var mu sync.Mutex
	var edits []serviceEdit
	var syncs []serviceSync
	var unsynced []model.UnsyncedConfig
	var errList []error

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(podsConcurrent)
	for _, app := range apps {
		eg.Go(func() error {
			entries, err := u.kusec.ListAudit(egCtx, &kusecModel.AuditReq{AppId: app.id, Since: since, Until: until, Limit: u.conf.MaxEvents})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errList = append(errList, fmt.Errorf("audit: %w", err))
				return nil
			}
			edits = append(edits, lo.FilterMap(entries, func(e kusecModel.AuditEntry, _ int) (serviceEdit, bool) {
				edit, ok := decodeAuditEntry(e, app)
				return serviceEdit{ConfigEdit: edit, service: app.service}, ok
			})...)
			return nil
		})
		eg.Go(func() error {
			runs, err := u.kusec.ListSyncRuns(egCtx, &kusecModel.SyncRunReq{AppId: app.id, Since: since, Until: until, Limit: maxSyncRuns})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errList = append(errList, fmt.Errorf("sync-run: %w", err))
				return nil
			}
			syncs = append(syncs, lo.FilterMap(runs, func(r kusecModel.SyncRun, _ int) (serviceSync, bool) {
				s, ok := decodeSyncRun(r, app)
				return serviceSync{ConfigSync: s, service: app.service}, ok
			})...)
			return nil
		})
		if withDrift {
			eg.Go(func() error {
				drift, err := u.kusec.GetDrift(egCtx, app.id)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					errList = append(errList, fmt.Errorf("drift: %w", err))
					return nil
				}
				unsynced = append(unsynced, decodeDrift(drift, app)...)
				return nil
			})
		}
	}
	_ = eg.Wait()

	return edits, syncs, unsynced, errors.Join(errList...)
}

// kusecApps — приложения kusec по configmap/secret, подключённым к подам сервиса.
func (u *Usecase) kusecApps(ctx context.Context, workloads []*workloadModel.Main) ([]*kusecApp, error) {
	byId := make(map[string]*kusecApp)
	for _, w := range workloads {
		for _, ref := range w.ConfigRefs {
			resolved, err := u.kusec.Resolve(ctx, w.Namespace, ref)
			if err != nil {
				return nil, fmt.Errorf("resolve %s/%s: %w", w.Namespace, ref, err)
			}
			if !resolved.Found {
				continue
			}
			app, ok := byId[resolved.AppId]
			if !ok {
				app = &kusecApp{id: resolved.AppId, service: w.ServiceName, objects: map[string]struct{}{}}
				byId[resolved.AppId] = app
			}
			app.objects[ref] = struct{}{}
		}
	}
	return lo.Values(byId), nil
}

// decodeAuditEntry — запись аудита как правка конфигурации сервиса с маскированием (ТЗ 4.2):
// значение секрета — только факт изменения, обычный конфиг — через redact.Value.
func decodeAuditEntry(e kusecModel.AuditEntry, app *kusecApp) (eventModel.ConfigEdit, bool) {
	switch e.EntityType {
	case kusecModel.EntityItem, kusecModel.EntityConfigItem, kusecModel.EntitySecret, kusecModel.EntityConfigMap:
		if _, own := app.objects[e.KubeName]; !own {
			return eventModel.ConfigEdit{}, false
		}
	case kusecModel.EntityApp:
	default:
		return eventModel.ConfigEdit{}, false // sync — отдельно, api_key/usr — не конфигурация сервиса
	}

	edit := eventModel.ConfigEdit{
		TS: e.CreatedAt, Author: e.ActorName, Origin: e.Source, Action: e.Action,
		ObjectKind: lo.CoalesceOrEmpty(strings.ToLower(e.KubeKind), e.EntityType), ObjectName: lo.CoalesceOrEmpty(e.KubeName, e.AppSlug),
		Key: e.Key,
	}
	secret := e.KubeKind == kusecModel.KubeKindSecret

	for _, ch := range e.Changes {
		if ch.Field != kusecModel.ValueField {
			edit.Fields = append(edit.Fields, ch.Field)
			continue
		}
		edit.ValueChanged = true
		switch {
		case secret:
			edit.OldValue, edit.NewValue = redact.Secret(), redact.Secret()
		case ch.Truncated:
			edit.OldValue, edit.NewValue = sizeMarker(ch.OldSize), sizeMarker(ch.NewSize)
		default:
			edit.OldValue, edit.NewValue = redact.Value(e.Key, lo.FromPtr(ch.Old)), redact.Value(e.Key, lo.FromPtr(ch.New))
		}
	}
	return edit, true
}

func sizeMarker(size *int64) string {
	if size == nil {
		return ""
	}
	return fmt.Sprintf("<%d байт>", *size)
}

// decodeSyncRun — запуск sync с объектами сервиса, которые он действительно изменил.
func decodeSyncRun(r kusecModel.SyncRun, app *kusecApp) (eventModel.ConfigSync, bool) {
	objects := lo.FilterMap(r.Objects, func(o kusecModel.SyncObject, _ int) (eventModel.ConfigSyncObject, bool) {
		_, own := app.objects[o.KubeName]
		return eventModel.ConfigSyncObject{ObjectKind: strings.ToLower(o.KubeKind), ObjectName: o.KubeName, Op: o.Op, ChangedKeys: o.ChangedKeys},
			own && o.Op != kusecModel.SyncOpUnchanged
	})
	if len(objects) == 0 {
		return eventModel.ConfigSync{}, false
	}
	return eventModel.ConfigSync{RunId: r.Id, TS: r.StartedAt, Author: r.ActorName, Status: r.Status, Error: r.Error, Objects: objects}, true
}

func decodeDrift(d *kusecModel.Drift, app *kusecApp) []model.UnsyncedConfig {
	if d == nil || !d.InCluster {
		return nil
	}
	return lo.FilterMap(d.Objects, func(o kusecModel.DriftObject, _ int) (model.UnsyncedConfig, bool) {
		_, own := app.objects[o.KubeName]
		return model.UnsyncedConfig{
			Kind: strings.ToLower(o.KubeKind), Object: o.KubeName, NotSyncedSince: o.NotSyncedSince, ExistsInCluster: o.ExistsInCluster,
			MissingInCluster: o.MissingInCluster, ExtraInCluster: o.ExtraInCluster, ValueDiffers: o.ValueDiffers,
		}, own && o.HasDrift()
	})
}
