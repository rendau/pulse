package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/rendau/pulse/internal/domain/cluster/model"
	"github.com/rendau/pulse/internal/util/window"
)

// PublicConfig — пороги проблем публичных приложений gateway (за окно get_cluster_health).
type PublicConfig struct {
	// MinRequests — с этого числа запросов за окно оцениваются сбои и задержка
	MinRequests float64
	// MinErrors, ErrorRate, ErrorFactor — всплеск 5xx: не меньше MinErrors ответов, доля не ниже
	// ErrorRate и в ErrorFactor раз выше обычной (за сутки до окна)
	MinErrors   float64
	ErrorRate   float64
	ErrorFactor float64
	// SlowP95, SlowFactor — медленно: p95 не ниже SlowP95 и в SlowFactor раз выше обычного
	SlowP95    time.Duration
	SlowFactor float64
	// TrafficMin, TrafficDrop — трафик пропал: окном раньше и вчера в это время было не меньше
	// TrafficMin запросов, а сейчас не больше TrafficDrop от меньшего из них
	TrafficMin  float64
	TrafficDrop float64
	// BackendErrorsMin — столько раз backend не ответил gateway за окно — проблема
	BackendErrorsMin int
}

func (c *PublicConfig) defaults() {
	c.MinRequests = lo.Ternary(c.MinRequests > 0, c.MinRequests, 50)
	c.MinErrors = lo.Ternary(c.MinErrors > 0, c.MinErrors, 10)
	c.ErrorRate = lo.Ternary(c.ErrorRate > 0, c.ErrorRate, 0.05)
	c.ErrorFactor = lo.Ternary(c.ErrorFactor > 0, c.ErrorFactor, 3)
	c.SlowP95 = lo.Ternary(c.SlowP95 > 0, c.SlowP95, 2*time.Second)
	c.SlowFactor = lo.Ternary(c.SlowFactor > 0, c.SlowFactor, 2)
	c.TrafficMin = lo.Ternary(c.TrafficMin > 0, c.TrafficMin, 300)
	c.TrafficDrop = lo.Ternary(c.TrafficDrop > 0, c.TrafficDrop, 0.02)
	c.BackendErrorsMin = lo.Ternary(c.BackendErrorsMin > 0, c.BackendErrorsMin, 5)
}

// PublicProblems — проблемы каждого приложения по его трафику, ошибкам gateway и подам backend'а;
// остаются только приложения с проблемой.
func (s *Service) PublicProblems(apps []*model.PublicApp, win time.Duration) []*model.PublicApp {
	return lo.Filter(apps, func(app *model.PublicApp, _ int) bool {
		app.Problems = s.publicProblems(app, window.Format(win))
		return len(app.Problems) > 0
	})
}

func (s *Service) publicProblems(app *model.PublicApp, win string) []model.PublicProblem {
	c := s.conf.Public
	var result []model.PublicProblem
	add := func(kind, format string, args ...any) {
		result = append(result, model.PublicProblem{Kind: kind, Text: fmt.Sprintf(format, args...)})
	}

	if t := app.Traffic; t != nil {
		usualRate := lo.FromPtr(t.UsualErrorRate)
		if t.Requests >= c.MinRequests && t.Errors >= c.MinErrors && t.ErrorRate != nil &&
			*t.ErrorRate >= c.ErrorRate && *t.ErrorRate >= c.ErrorFactor*usualRate {
			add(model.PublicErrors, "сбои (5xx) — %s запросов (%.0f из %.0f) за %s, обычно %s",
				percent(*t.ErrorRate), t.Errors, t.Requests, win, percent(usualRate))
		}
	}

	if total := lo.SumBy(app.BackendErrors, func(r model.GatewayReason) int { return r.Count }); total >= c.BackendErrorsMin {
		add(model.PublicBackend, "backend не ответил gateway %d раз за %s: %s", total, win,
			strings.Join(lo.Map(app.BackendErrors, func(r model.GatewayReason, _ int) string {
				return fmt.Sprintf("%s ×%d", r.Reason, r.Count)
			}), ", "))
	}
	if p := app.BackendPods; p != nil && p.Desired > 0 && p.Ready == 0 {
		add(model.PublicBackend, "у backend'а %s нет готовых подов (нужно %d) — маршрут ведёт в пустоту", app.Service, p.Desired)
	}

	for _, e := range app.ScriptErrors {
		text := fmt.Sprintf("скрипт маршрута %s: %s ×%d", lo.CoalesceOrEmpty(e.Route, "?"), e.Reason, e.Count)
		if e.Example != "" {
			text += " — " + e.Example
		}
		add(model.PublicScript, "%s", text)
	}

	if t := app.Traffic; t != nil && t.Requests >= c.MinRequests && t.P95 != nil && t.UsualP95 != nil &&
		*t.P95 >= c.SlowP95.Seconds() && *t.P95 >= c.SlowFactor**t.UsualP95 {
		add(model.PublicSlow, "медленно — p95 %s за %s, обычно %s", seconds(*t.P95), win, seconds(*t.UsualP95))
	}

	if t := app.Traffic; t != nil && t.PrevRequests != nil && t.YesterdayRequests != nil {
		usual := min(*t.PrevRequests, *t.YesterdayRequests)
		if usual >= c.TrafficMin && t.Requests <= c.TrafficDrop*usual {
			add(model.PublicNoTraffic, "трафик пропал — %.0f запросов за %s, окном раньше %.0f, вчера в это время %.0f",
				t.Requests, win, *t.PrevRequests, *t.YesterdayRequests)
		}
	}

	return result
}

func percent(v float64) string {
	switch {
	case v == 0:
		return "0%"
	case v < 0.01:
		return fmt.Sprintf("%.2f%%", v*100)
	}
	return fmt.Sprintf("%.0f%%", v*100)
}

func seconds(v float64) string {
	if v < 1 {
		return fmt.Sprintf("%.0f мс", v*1000)
	}
	return fmt.Sprintf("%.1f с", v)
}
