package dto

import (
	"time"

	"github.com/samber/lo"

	systemModel "github.com/mechta-market/pulse/internal/usecase/system/model"
	"github.com/mechta-market/pulse/internal/util/tz"
)

// EmptyReq — инструмент без параметров.
type EmptyReq struct{}

type PingRep struct {
	Version     string         `json:"version"`
	GeneratedAt time.Time      `json:"generated_at"`
	Sources     []SourceStatus `json:"sources"`
}

type SourceStatus struct {
	Name      string `json:"name"`
	Status    string `json:"status" jsonschema:"ok | error | disabled"`
	Error     string `json:"error,omitempty"`
	LatencyMs int64  `json:"latency_ms,omitempty"`
}

func EncodePing(v *systemModel.Ping) PingRep {
	return PingRep{
		Version:     v.Version,
		GeneratedAt: tz.In(v.GeneratedAt),
		Sources:     lo.Map(v.Sources, encodeSourceStatus),
	}
}

func encodeSourceStatus(v systemModel.SourceStatus, _ int) SourceStatus {
	return SourceStatus{
		Name:      v.Name,
		Status:    v.Status,
		Error:     v.Error,
		LatencyMs: v.Latency.Milliseconds(),
	}
}
