package dto

import (
	"time"

	"github.com/samber/lo"

	deployModel "github.com/mechta-market/pulse/internal/domain/deploy/model"
	usecaseTimelineModel "github.com/mechta-market/pulse/internal/usecase/timeline/model"
	"github.com/mechta-market/pulse/internal/util/window"
)

// get_timeline

type GetTimelineReq struct {
	Service  string   `json:"service,omitempty" jsonschema:"точное имя сервиса"`
	Services []string `json:"services,omitempty" jsonschema:"несколько сервисов сразу"`
	Scope    string   `json:"scope,omitempty" jsonschema:"cluster — изменения по всему кластеру (без коммитов и конфигурации)"`
	Window   string   `json:"window,omitempty" jsonschema:"Go duration: 1h, 24h (по умолчанию), 7d (максимум)"`
}

type TimelineRep struct {
	Services   []string      `json:"services"`
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
	Unreleased    *Unreleased    `json:"unreleased,omitempty" jsonschema:"смержено, но ещё не в проде; отсутствует, если задеплоенный коммит неизвестен"`
	Deploys       []Deploy       `json:"deploys"`
	ConfigChanges []ConfigChange `json:"config_changes" jsonschema:"значения секретов никогда не возвращаются; env/configmap — только безопасные значения, остальное ***"`
	Errors        []SourceError  `json:"errors"`
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
	TS       time.Time `json:"ts"`
	Source   string    `json:"source" jsonschema:"kusec — изменение ключа; reloader — поды перекачены после смены configmap/secret (key — имя объекта, значения — отпечатки содержимого)"`
	Workload string    `json:"workload,omitempty"`
	Kind     string    `json:"kind" jsonschema:"configmap | secret | env"`
	Key      string    `json:"key"`
	OldValue string    `json:"old_value"`
	NewValue string    `json:"new_value"`
	Author   string    `json:"author,omitempty"`
}

func EncodeChangesRep(v *usecaseTimelineModel.ChangesResult) ChangesRep {
	rep := ChangesRep{
		Service:       v.Service,
		Window:        window.Format(v.Window),
		Commits:       lo.Map(v.Commits, encodeCommit),
		Deploys:       lo.Map(v.Deploys, encodeDeploy),
		ConfigChanges: lo.Map(v.ConfigChanges, encodeConfigChange),
		Errors:        lo.Map(v.Errors, encodeTimelineSourceError),
	}
	if v.Unreleased != nil {
		rep.Unreleased = &Unreleased{
			DeployedCommit: v.Unreleased.DeployedCommit,
			BehindBy:       v.Unreleased.BehindBy,
			Commits:        lo.Map(v.Unreleased.Commits, encodeCommit),
		}
	}
	return rep
}

func encodeCommit(v usecaseTimelineModel.Commit, _ int) Commit {
	return Commit{SHA: v.SHA, Author: v.Author, Message: v.Message, Date: v.Date.UTC(), Url: v.Url}
}

func encodeDeploy(v *deployModel.Main, _ int) Deploy {
	return Deploy{
		ObservedAt: v.ObservedAt.UTC(), Workload: v.Kind + "/" + v.Namespace + "/" + v.Name,
		Image: v.Image, ImageDigest: v.ImageDigest, DeployedCommit: v.DeployedCommit,
		PrevImage: v.PrevImage, PrevImageDigest: v.PrevImageDigest, PrevCommit: v.PrevCommit,
	}
}

func encodeConfigChange(v usecaseTimelineModel.ConfigChange, _ int) ConfigChange {
	return ConfigChange{TS: v.TS.UTC(), Source: v.Source, Workload: v.Workload, Kind: v.Kind, Key: v.Key, OldValue: v.OldValue, NewValue: v.NewValue, Author: v.Author}
}

func encodeTimelineSourceError(v usecaseTimelineModel.SourceError, _ int) SourceError {
	return SourceError{Source: v.Source, Message: v.Message}
}
