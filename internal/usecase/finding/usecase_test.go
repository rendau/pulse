package finding

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/pulse/internal/errs"
	pulseagentModel "github.com/rendau/pulse/internal/service/pulseagent/model"
	"github.com/rendau/pulse/internal/usecase/finding/model"
)

type fakeAgent struct {
	got *pulseagentModel.Finding
	err error
}

func (f *fakeAgent) SendFinding(_ context.Context, finding *pulseagentModel.Finding) (*pulseagentModel.FindingResult, error) {
	f.got = finding
	if f.err != nil {
		return nil, f.err
	}
	return &pulseagentModel.FindingResult{Id: 7, Deduplicated: true}, nil
}

func TestReport(t *testing.T) {
	agent := &fakeAgent{}
	rep, err := New(agent).Report(context.Background(), &model.ReportReq{
		Ip:       "2a03:32c0:e000:2545:d4bc:27ff:fede:f157",
		Severity: " Critical ",
		Title:    "Перебор СМС-кодов‮ с одного адреса\n" + strings.Repeat("я", 200),
		Text:     "15 тыс. попыток за 13 минут,\nзатем ручки прав.\x00\n\n\n\nСтрок с путями нет.\n" + strings.Repeat("x", 3000),
	})
	require.NoError(t, err)
	assert.Equal(t, &model.Report{Key: "security:2a03:32c0:e000:2545::/64", Id: 7, Deduplicated: true}, rep)

	got := agent.got
	assert.Equal(t, "security", got.Kind)
	assert.Equal(t, "critical", got.Severity)
	assert.Equal(t, "claude-routine", got.Source)
	assert.Equal(t, MaxTitleLen, len([]rune(got.Title)))
	assert.True(t, strings.HasPrefix(got.Title, "Перебор СМС-кодов с одного адреса я"), got.Title)
	assert.LessOrEqual(t, len([]rune(got.Text)), MaxTextLen)
	assert.True(t, strings.HasPrefix(got.Text, "15 тыс. попыток за 13 минут,\nзатем ручки прав.\n\nСтрок с путями нет."), got.Text)
}

func TestReportValidation(t *testing.T) {
	ok := model.ReportReq{Ip: "198.51.100.7", Severity: "warning", Title: "t", Text: "x"}
	cases := map[string]func(r *model.ReportReq){
		"ip not address": func(r *model.ReportReq) { r.Ip = "evil.example.com" },
		"ip empty":       func(r *model.ReportReq) { r.Ip = "" },
		"ipv4 subnet":    func(r *model.ReportReq) { r.Ip = "198.51.100.0/24" },
		"ipv6 /48":       func(r *model.ReportReq) { r.Ip = "2a03:32c0::/48" },
		"severity":       func(r *model.ReportReq) { r.Severity = "info" },
		"title empty":    func(r *model.ReportReq) { r.Title = "\x00 \n" },
		"text empty":     func(r *model.ReportReq) { r.Text = "" },
	}
	for name, mutate := range cases {
		agent := &fakeAgent{}
		req := ok
		mutate(&req)
		_, err := New(agent).Report(context.Background(), &req)
		require.ErrorIs(t, err, errs.InvalidRequest, name)
		assert.Nil(t, agent.got, name)
	}
}

func TestReportRateLimited(t *testing.T) {
	agent := &fakeAgent{err: fmt.Errorf("%w: x", pulseagentModel.ErrRateLimited)}
	_, err := New(agent).Report(context.Background(), &model.ReportReq{Ip: "198.51.100.7", Severity: "warning", Title: "t", Text: "x"})
	full, ok := errs.AsErrFull(err)
	require.True(t, ok)
	assert.Contains(t, full.Desc, "лимит находок в час исчерпан")
}
