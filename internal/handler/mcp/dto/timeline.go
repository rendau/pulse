package dto

import (
	"time"

	"github.com/samber/lo"

	deployModel "github.com/rendau/pulse/internal/domain/deploy/model"
	usecaseTimelineModel "github.com/rendau/pulse/internal/usecase/timeline/model"
	"github.com/rendau/pulse/internal/util/tz"
	"github.com/rendau/pulse/internal/util/window"
)

// get_timeline

type GetTimelineReq struct {
	Service  string   `json:"service,omitempty" jsonschema:"точное имя сервиса"`
	Services []string `json:"services,omitempty" jsonschema:"несколько сервисов сразу"`
	Scope    string   `json:"scope,omitempty" jsonschema:"cluster — изменения по всему кластеру (без коммитов и правок kusec)"`
	Window   string   `json:"window,omitempty" jsonschema:"Go duration: 1h, 24h (по умолчанию), 7d (максимум)"`
}

type TimelineRep struct {
	Services   []string      `json:"services" jsonschema:"сервисы запроса; для scope=cluster — только сервисы с событиями за окно, самые свежие первыми"`
	Window     string        `json:"window"`
	Events     []Event       `json:"events" jsonschema:"по убыванию времени: deploy | commit | config_change | alert_firing | scale | restart | oom_kill | warning"`
	TotalCount int           `json:"total_count"`
	Truncated  bool          `json:"truncated"`
	Errors     []SourceError `json:"errors" jsonschema:"источники, которые не ответили или не подключены"`
}

func EncodeTimelineRep(v *usecaseTimelineModel.TimelineResult) TimelineRep {
	return TimelineRep{
		Services:   v.Services,
		Window:     window.Format(v.Window),
		Events:     lo.Map(v.Events, EncodeEvent),
		TotalCount: v.TotalCount,
		Truncated:  v.Truncated,
		Errors:     lo.Map(v.Errors, encodeTimelineSourceError),
	}
}

// get_changes

type GetChangesReq struct {
	Service string `json:"service" jsonschema:"точное имя сервиса"`
	Window  string `json:"window,omitempty" jsonschema:"Go duration: 1h, 24h (по умолчанию), 7d (максимум)"`
}

type ChangesRep struct {
	Service       string         `json:"service"`
	Window        string         `json:"window"`
	Commits       []Commit       `json:"commits" jsonschema:"коммиты ветки по умолчанию за окно, новые первыми"`
	LastCommit    *Commit        `json:"last_commit,omitempty" jsonschema:"последний коммит ветки по умолчанию — есть, только если за окно коммитов нет"`
	Unreleased    *Unreleased    `json:"unreleased,omitempty" jsonschema:"смержено, но ещё не в проде; отсутствует, если задеплоенный коммит неизвестен"`
	Deploys       []Deploy       `json:"deploys"`
	ConfigChanges []ConfigChange `json:"config_changes" jsonschema:"по убыванию времени; значения секретов никогда не возвращаются, обычный конфиг — только безопасные значения, остальное ***"`
	// UnsyncedConfig — nil, если kusec не подключён
	UnsyncedConfig []UnsyncedConfig `json:"unsynced_config,omitempty" jsonschema:"конфигурация в kusec расходится с кластером: изменение не применено или значения отличаются"`
	Errors         []SourceError    `json:"errors"`
}

type Commit struct {
	SHA     string    `json:"sha"`
	Author  string    `json:"author"`
	Message string    `json:"message"`
	Date    time.Time `json:"date"`
	Url     string    `json:"url,omitempty"`
}

type Unreleased struct {
	DeployedCommit string   `json:"deployed_commit"`
	BehindBy       int      `json:"behind_by" jsonschema:"на сколько коммитов прод отстаёт от ветки по умолчанию"`
	Commits        []Commit `json:"commits"`
}

type Deploy struct {
	ObservedAt      time.Time `json:"observed_at" jsonschema:"когда индексер заметил смену образа (точность — интервал индексации)"`
	Workload        string    `json:"workload"`
	Image           string    `json:"image"`
	ImageDigest     string    `json:"image_digest,omitempty"`
	DeployedCommit  string    `json:"deployed_commit,omitempty"`
	PrevImage       string    `json:"prev_image,omitempty"`
	PrevImageDigest string    `json:"prev_image_digest,omitempty"`
	PrevCommit      string    `json:"prev_commit,omitempty"`
}

type ConfigChange struct {
	TS          time.Time `json:"ts"`
	Source      string    `json:"source" jsonschema:"kusec — правка в kusec (ещё не в кластере до sync); kusec_sync — kusec применил объект в кластер; reloader — поды перекачены после смены объекта (с автором и ключами, если найден sync kusec)"`
	Action      string    `json:"action,omitempty"`
	Kind        string    `json:"kind" jsonschema:"configmap | secret | app"`
	Object      string    `json:"object" jsonschema:"имя k8s-объекта (kusec-<app>-main)"`
	Key         string    `json:"key,omitempty"`
	OldValue    string    `json:"old_value,omitempty" jsonschema:"значение обычного конфига (небезопасное — ***) или отпечаток содержимого у reloader; секреты — всегда ***"`
	NewValue    string    `json:"new_value,omitempty"`
	Fields      []string  `json:"fields,omitempty" jsonschema:"прочие изменённые поля ключа"`
	ChangedKeys []string  `json:"changed_keys,omitempty" jsonschema:"ключи, применённые в кластер"`
	Author      string    `json:"author,omitempty"`
	Workload    string    `json:"workload,omitempty"`
	SyncRunId   string    `json:"sync_run_id,omitempty"`
	Status      string    `json:"status,omitempty" jsonschema:"статус sync, если не ok"`
}

type UnsyncedConfig struct {
	Kind             string     `json:"kind"`
	Object           string     `json:"object"`
	NotSyncedSince   *time.Time `json:"not_synced_since,omitempty" jsonschema:"правка в kusec с этого момента не применена в кластер"`
	ExistsInCluster  bool       `json:"exists_in_cluster"`
	MissingInCluster []string   `json:"missing_in_cluster,omitempty"`
	ExtraInCluster   []string   `json:"extra_in_cluster,omitempty"`
	ValueDiffers     []string   `json:"value_differs,omitempty" jsonschema:"ключи, значение которых в кластере отличается от kusec (только имена)"`
}

func EncodeChangesRep(v *usecaseTimelineModel.ChangesResult) ChangesRep {
	rep := ChangesRep{
		Service:       v.Service,
		Window:        window.Format(v.Window),
		Commits:       lo.Map(v.Commits, encodeCommit),
		Deploys:       lo.Map(v.Deploys, encodeDeploy),
		ConfigChanges: lo.Map(v.ConfigChanges, encodeConfigChange),
		UnsyncedConfig: lo.Map(v.UnsyncedConfig, func(u usecaseTimelineModel.UnsyncedConfig, _ int) UnsyncedConfig {
			return UnsyncedConfig{
				Kind: u.Kind, Object: u.Object, NotSyncedSince: tz.InPtr(u.NotSyncedSince), ExistsInCluster: u.ExistsInCluster,
				MissingInCluster: u.MissingInCluster, ExtraInCluster: u.ExtraInCluster, ValueDiffers: u.ValueDiffers,
			}
		}),
		Errors: lo.Map(v.Errors, encodeTimelineSourceError),
	}
	if v.Unreleased != nil {
		rep.Unreleased = &Unreleased{
			DeployedCommit: v.Unreleased.DeployedCommit,
			BehindBy:       v.Unreleased.BehindBy,
			Commits:        lo.Map(v.Unreleased.Commits, encodeCommit),
		}
	}
	if v.LastCommit != nil {
		rep.LastCommit = new(encodeCommit(*v.LastCommit, 0))
	}
	return rep
}

func encodeCommit(v usecaseTimelineModel.Commit, _ int) Commit {
	return Commit{SHA: v.SHA, Author: v.Author, Message: v.Message, Date: tz.In(v.Date), Url: v.Url}
}

func encodeDeploy(v *deployModel.Main, _ int) Deploy {
	return Deploy{
		ObservedAt: tz.In(v.ObservedAt), Workload: v.Kind + "/" + v.Namespace + "/" + v.Name,
		Image: v.Image, ImageDigest: v.ImageDigest, DeployedCommit: v.DeployedCommit,
		PrevImage: v.PrevImage, PrevImageDigest: v.PrevImageDigest, PrevCommit: v.PrevCommit,
	}
}

func encodeConfigChange(v usecaseTimelineModel.ConfigChange, _ int) ConfigChange {
	return ConfigChange{
		TS: tz.In(v.TS), Source: v.Source, Action: v.Action, Kind: v.Kind, Object: v.Object, Key: v.Key,
		OldValue: v.OldValue, NewValue: v.NewValue, Fields: v.Fields, ChangedKeys: v.ChangedKeys,
		Author: v.Author, Workload: v.Workload, SyncRunId: v.SyncRunId, Status: v.Status,
	}
}

func encodeTimelineSourceError(v usecaseTimelineModel.SourceError, _ int) SourceError {
	return SourceError{Source: v.Source, Message: v.Message}
}
