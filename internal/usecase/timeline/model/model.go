package model

import (
	"time"

	deployModel "github.com/mechta-market/pulse/internal/domain/deploy/model"
	eventModel "github.com/mechta-market/pulse/internal/domain/event/model"
)

const ScopeCluster = "cluster"

// TimelineReq — сервис(ы) либо scope=cluster.
type TimelineReq struct {
	Services []string
	Scope    string
	Window   time.Duration
}

type TimelineResult struct {
	Services   []string
	Window     time.Duration
	Events     []eventModel.Event
	TotalCount int
	Truncated  bool
	Errors     []SourceError
}

// Commit — коммит ветки по умолчанию.
type Commit struct {
	SHA     string
	Author  string
	Message string
	Date    time.Time
	Url     string
}

// Unreleased — смержено, но ещё не в проде.
type Unreleased struct {
	DeployedCommit string
	BehindBy       int
	Commits        []Commit
}

// ConfigChange — изменение конфигурации с уже применённым маскированием.
type ConfigChange struct {
	TS       time.Time
	Kind     string // configmap | secret | env
	Key      string
	OldValue string
	NewValue string
	Author   string
}

type ChangesResult struct {
	Service       string
	Window        time.Duration
	Commits       []Commit
	Unreleased    *Unreleased
	Deploys       []*deployModel.Main
	ConfigChanges []ConfigChange
	Errors        []SourceError
}

type SourceError struct {
	Source  string
	Message string
}
