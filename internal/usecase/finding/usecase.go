// Package finding — находки об атаках: агент по расписанию (коннектор pulse на claude.ai) нашёл
// атаку в журнале входящих запросов и передаёт её в ленту pulse-agent (оттуда — Telegram чатам с
// подпиской на kind=security). Единственная запись pulse наружу: текст — от модели, а она читала
// текст атакующего, поэтому он чистится и обрезается здесь, а адрес проверяется.
package finding

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/samber/lo"

	edgeService "github.com/rendau/pulse/internal/domain/edge/service"
	"github.com/rendau/pulse/internal/errs"
	pulseagentModel "github.com/rendau/pulse/internal/service/pulseagent/model"
	"github.com/rendau/pulse/internal/usecase/finding/model"
)

const (
	Kind   = "security"
	Source = "claude-routine"

	MaxTitleLen = 120
	MaxTextLen  = 2000
)

// Severities — уровни находки: warning — подозрительно, critical — атака идёт или удалась.
var Severities = []string{"warning", "critical"}

type FindingI interface {
	Report(ctx context.Context, req *model.ReportReq) (*model.Report, error)
}

// ports

type pulseAgentI interface {
	SendFinding(ctx context.Context, finding *pulseagentModel.Finding) (*pulseagentModel.FindingResult, error)
}

type Usecase struct {
	agent pulseAgentI
}

func New(agent pulseAgentI) *Usecase {
	return &Usecase{agent: agent}
}

func (u *Usecase) Report(ctx context.Context, req *model.ReportReq) (*model.Report, error) {
	// ключ склейки — адрес как в get_edge_traffic: повторы по одному адресу (и по одной подсети
	// IPv6 /64) — одна запись ленты
	addr, ok := edgeService.ClientKey(req.Ip)
	if !ok || (strings.Contains(addr, "/") && (!strings.Contains(addr, ":") || !strings.HasSuffix(addr, "/64"))) {
		return nil, fmt.Errorf("%w: ip %q: expected IPv4/IPv6 address or IPv6 /64 subnet (addr from get_edge_traffic)", errs.InvalidRequest, req.Ip)
	}
	severity := strings.ToLower(strings.TrimSpace(req.Severity))
	if !lo.Contains(Severities, severity) {
		return nil, fmt.Errorf("%w: severity %q: expected warning or critical", errs.InvalidRequest, req.Severity)
	}
	title := edgeService.Clean(req.Title, MaxTitleLen)
	text := edgeService.CleanLines(req.Text, MaxTextLen)
	if title == "" || text == "" {
		return nil, fmt.Errorf("%w: title and text are required", errs.InvalidRequest)
	}

	key := Kind + ":" + addr
	rep, err := u.agent.SendFinding(ctx, &pulseagentModel.Finding{
		Key: key, Kind: Kind, Severity: severity, Title: title, Text: text, Source: Source,
	})
	if err != nil {
		if errors.Is(err, pulseagentModel.ErrRateLimited) {
			return nil, errs.ErrFull{Err: errs.ServiceNA, Desc: "лимит находок в час исчерпан (pulse-agent ответил 429): " +
				"находка не отправлена, повторять в этот час не нужно — укажи её в своём отчёте"}
		}
		return nil, fmt.Errorf("agent.SendFinding: %w", err)
	}
	return &model.Report{Key: key, Id: rep.Id, Deduplicated: rep.Deduplicated}, nil
}
