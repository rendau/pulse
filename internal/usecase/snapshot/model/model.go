package model

import (
	"time"

	snapshotModel "github.com/mechta-market/pulse/internal/domain/snapshot/model"
)

// QueryMetricsReq — drill-down по метрике: приоритет у MetricId из service.yaml
// (или дефолтного набора), произвольный PromQL — fallback с ограничениями.
type QueryMetricsReq struct {
	Service  string
	MetricId string
	PromQL   string
	Window   time.Duration
	Step     time.Duration
}

type QueryMetricsResult struct {
	Service string
	Def     snapshotModel.MetricDef
	Start   time.Time
	End     time.Time
	Step    time.Duration
	Series  []snapshotModel.Series
}
