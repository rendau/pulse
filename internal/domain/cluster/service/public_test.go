package service

import (
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"

	"github.com/rendau/pulse/internal/domain/cluster/model"
)

func TestPublicProblems(t *testing.T) {
	s := New(Config{})
	kinds := func(app *model.PublicApp) []string {
		apps := s.PublicProblems([]*model.PublicApp{app}, 15*time.Minute)
		if len(apps) == 0 {
			return nil
		}
		return lo.Map(apps[0].Problems, func(p model.PublicProblem, _ int) string { return p.Kind })
	}
	traffic := func(requests, errors float64, usualRate *float64) *model.PublicTraffic {
		return &model.PublicTraffic{Requests: requests, Errors: errors, ErrorRate: new(errors / requests), UsualErrorRate: usualRate}
	}

	assert.Equal(t, []string{model.PublicErrors}, kinds(&model.PublicApp{Traffic: traffic(1000, 100, new(0.001))}))
	assert.Equal(t, []string{model.PublicErrors}, kinds(&model.PublicApp{Traffic: traffic(1000, 100, nil)}), "нет истории — обычный уровень 0")
	assert.Nil(t, kinds(&model.PublicApp{Traffic: traffic(1000, 100, new(0.08))}), "всегда так: 10% при обычных 8%")
	assert.Nil(t, kinds(&model.PublicApp{Traffic: traffic(1000, 30, new(0.0))}), "3% — ниже порога доли")
	assert.Nil(t, kinds(&model.PublicApp{Traffic: traffic(20, 8, new(0.0))}), "мало запросов")

	assert.Nil(t, kinds(&model.PublicApp{BackendErrors: []model.GatewayReason{{Reason: "backend closed connection", Count: 1}}}), "единичный обрыв — шум")
	assert.Nil(t, kinds(&model.PublicApp{Service: "x", BackendPods: &model.PodsReady{Ready: 0, Desired: 0}}), "выключен намеренно")
	assert.Nil(t, kinds(&model.PublicApp{Service: "x", BackendPods: &model.PodsReady{Ready: 1, Desired: 3}}), "часть подов готова")

	slow := func(p95, usual float64) *model.PublicApp {
		return &model.PublicApp{Traffic: &model.PublicTraffic{Requests: 500, P95: new(p95), UsualP95: new(usual)}}
	}
	assert.Equal(t, []string{model.PublicSlow}, kinds(slow(2.5, 0.3)))
	assert.Nil(t, kinds(slow(2.5, 2)), "всегда медленное")
	assert.Nil(t, kinds(slow(0.9, 0.1)), "ниже абсолютного порога")

	drop := func(now, prev, yesterday float64) *model.PublicApp {
		return &model.PublicApp{Traffic: &model.PublicTraffic{Requests: now, PrevRequests: new(prev), YesterdayRequests: new(yesterday)}}
	}
	assert.Equal(t, []string{model.PublicNoTraffic}, kinds(drop(0, 500, 400)))
	assert.Nil(t, kinds(drop(0, 50, 400)), "окном раньше и так было мало (ночь)")
	assert.Nil(t, kinds(drop(100, 500, 400)), "упал, но не пропал")
}
