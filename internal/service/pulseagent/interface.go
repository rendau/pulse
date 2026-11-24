package pulseagent

import (
	"context"

	pulseagentModel "github.com/rendau/pulse/internal/service/pulseagent/model"
)

// Client — запись в pulse-agent: находки в его ленту (оттуда — бот и Telegram).
type Client interface {
	// SendFinding — POST /v1/findings; лимит находок в час исчерпан — pulseagentModel.ErrRateLimited.
	SendFinding(ctx context.Context, finding *pulseagentModel.Finding) (*pulseagentModel.FindingResult, error)
}
