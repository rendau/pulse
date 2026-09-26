// Package service — детерминированные правила снапшота: health, summary_hints, базовая
// линия метрик. Без обращения к источникам, чтобы правила были одинаковы от вызова к вызову
// и переиспользовались (фаза 5: здоровье соседа без полного снапшота).
package service

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/mechta-market/pulse/internal/constant"
	eventModel "github.com/mechta-market/pulse/internal/domain/event/model"
	"github.com/mechta-market/pulse/internal/domain/snapshot/model"
)

// Config — пороги правил.
type Config struct {
	// AnomalyThresholdPct — отклонение от вчера (в %), после которого метрика помечается anomaly
	AnomalyThresholdPct float64
}

type Service struct {
	conf Config
}

func New(conf Config) *Service {
	if conf.AnomalyThresholdPct <= 0 {
		conf.AnomalyThresholdPct = 30
	}
	return &Service{conf: conf}
}

// ComputeHealth применяет правила в порядке приоритета:
// нет данных о подах → unknown; ни одного ready-пода при желаемых > 0 → down;
// critical-алерт, неготовые поды, проблемные контейнеры или аномалия метрики → degraded.
func (s *Service) ComputeHealth(snap *model.Snapshot, podsUnavailable bool) string {
	if podsUnavailable {
		return model.HealthUnknown
	}

	runnable := lo.Filter(snap.Workloads, func(w model.WorkloadState, _ int) bool {
		return w.Kind != constant.WorkloadKindCronJob && w.ReplicasDesired > 0
	})
	if len(runnable) > 0 {
		totalReady := lo.SumBy(runnable, func(w model.WorkloadState) int { return w.Pods.Ready })
		if totalReady == 0 {
			return model.HealthDown
		}
	}

	for _, a := range snap.Alerts {
		if a.State == "active" && a.Severity == "critical" {
			return model.HealthDegraded
		}
	}
	// сервис сам сообщает о проблеме (зависимость down или деградировала)
	if snap.Self != nil && snap.Self.Status != "" && snap.Self.Status != "ok" {
		return model.HealthDegraded
	}
	for _, w := range runnable {
		if w.Pods.Ready < int(w.ReplicasDesired) || len(w.Pods.Problems) > 0 {
			return model.HealthDegraded
		}
	}
	for _, m := range snap.Metrics {
		if m.Anomaly {
			return model.HealthDegraded
		}
	}

	return model.HealthHealthy
}

// SummaryHints — факты, а не выводы: что именно не так, в порядке важности.
func (s *Service) SummaryHints(snap *model.Snapshot, now time.Time) []string {
	hints := make([]string, 0, 8)

	for _, a := range snap.Alerts {
		if a.State != "active" {
			continue
		}
		hint := fmt.Sprintf("алерт %s (%s) активен %s", a.Name, lo.CoalesceOrEmpty(a.Severity, "severity не задан"), humanSince(now, a.StartsAt))
		if a.Summary != "" {
			hint += ": " + a.Summary
		}
		hints = append(hints, hint)
	}

	hints = append(hints, selfHints(snap.Self, now)...)

	for _, w := range snap.Workloads {
		if w.Kind == constant.WorkloadKindCronJob {
			continue
		}
		if w.Pods.Ready < int(w.ReplicasDesired) {
			hints = append(hints, fmt.Sprintf("%s %s/%s: готово %d из %d подов",
				w.Kind, w.Namespace, w.Name, w.Pods.Ready, w.ReplicasDesired))
		}
		byReason := lo.GroupBy(w.Pods.Problems, func(p model.PodProblem) string { return p.Reason })
		reasons := lo.Keys(byReason)
		sort.Strings(reasons)
		for _, reason := range reasons {
			problems := byReason[reason]
			hints = append(hints, fmt.Sprintf("%s: %d контейнер(ов) в %s (%s)",
				w.Name, len(problems), reason, strings.Join(lo.Uniq(lo.Map(problems, func(p model.PodProblem, _ int) string { return p.Pod })), ", ")))
		}
		if !w.Pods.NewestStartedAt.IsZero() && now.Sub(w.Pods.NewestStartedAt) <= snap.Window {
			hints = append(hints, fmt.Sprintf("%s: самый свежий под запущен %s (выкатка или рестарт)",
				w.Name, humanSince(now, w.Pods.NewestStartedAt)))
		}
	}

	for _, m := range snap.Metrics {
		if !m.Anomaly || m.DeltaVsYesterday == nil {
			continue
		}
		verb := "вырос"
		if *m.DeltaVsYesterday < 0 {
			verb = "упал"
		}
		hints = append(hints, fmt.Sprintf("%s %s на %.0f%% относительно вчера (%s → %s)",
			m.Id, verb, math.Abs(*m.DeltaVsYesterday), formatValue(m.SameTimeYesterday), formatValue(m.Current)))
	}

	for i, p := range snap.TopErrors {
		if i >= 3 {
			break
		}
		hints = append(hints, fmt.Sprintf("в логах %d× «%s»", p.Count, lo.Ellipsis(p.Template, 160)))
	}

	oomCount := lo.CountBy(snap.RecentEvents, func(e eventModel.Event) bool { return e.Type == constant.EventTypeOOMKill })
	if oomCount > 0 {
		hints = append(hints, fmt.Sprintf("OOMKilled: %d событий за окно", oomCount))
	}

	for _, e := range snap.Errors {
		hints = append(hints, e.Hint())
	}

	return hints
}

// selfHints — что сервис сообщает о себе: не-ok зависимости (сначала критичные) и показатели.
func selfHints(self *model.SelfReport, now time.Time) []string {
	if self == nil {
		return nil
	}
	hints := make([]string, 0, 4)
	if self.Stale {
		hints = append(hints, fmt.Sprintf("отчёт сервиса о состоянии устарел: проверки выполнялись %s — фоновая проверка в сервисе, похоже, остановилась",
			humanSince(now, self.CheckedAt)))
	}
	deps := lo.Filter(self.Dependencies, func(d model.SelfDependency, _ int) bool { return d.Status != "" && d.Status != "ok" })
	sort.SliceStable(deps, func(i, j int) bool { return deps[i].Critical && !deps[j].Critical })
	for _, d := range deps {
		hint := fmt.Sprintf("сервис сообщает: зависимость %s (%s → %s%s) — %s", d.Id, d.Kind, d.Target,
			lo.Ternary(d.Critical, ", критичная", ""), d.Status)
		if d.LatencyMs != nil {
			hint += fmt.Sprintf(", %d мс", *d.LatencyMs)
		}
		if d.Message != "" {
			hint += ": " + d.Message
		}
		if d.Affects != "" {
			hint += "; ломает: " + d.Affects
		}
		hints = append(hints, hint)
	}
	for _, g := range self.Gauges {
		if g.Status == "" || g.Status == "ok" {
			continue
		}
		value := ""
		switch {
		case g.Value != nil:
			value = formatValue(g.Value) + " " + g.Unit
		case g.Time != nil:
			value = humanSince(now, *g.Time)
		}
		hints = append(hints, fmt.Sprintf("сервис сообщает: «%s» = %s (%s)", g.Title, strings.TrimSpace(value), g.Status))
	}
	for _, e := range self.Entities {
		for _, st := range e.Statuses {
			if st.Stuck <= 0 {
				continue
			}
			hint := fmt.Sprintf("сервис сообщает: застряло %d из %d «%s» в статусе %s", st.Stuck, st.Count, e.Name, st.Name)
			if st.Meaning != "" {
				hint += " (" + st.Meaning + ")"
			}
			if st.StuckAfter > 0 {
				hint += fmt.Sprintf(" — дольше %s", humanDuration(st.StuckAfter))
			}
			if st.Oldest > 0 {
				hint += fmt.Sprintf(", самый старый — %s", humanDuration(st.Oldest))
			}
			hints = append(hints, hint)
		}
	}
	return hints
}

// ApplyBaseline считает дельты и флаг аномалии. Аномалия — отклонение от вчера больше
// порога; если у метрики есть direction, учитывается только ухудшение.
func (s *Service) ApplyBaseline(m *model.Metric) {
	m.DeltaVsYesterday = deltaPct(m.Current, m.SameTimeYesterday)
	m.DeltaVsHourAgo = deltaPct(m.Current, m.HourAgo)

	if m.DeltaVsYesterday == nil {
		return
	}
	delta := *m.DeltaVsYesterday
	switch m.Direction {
	case constant.MetricDirectionHigherIsBetter:
		m.Anomaly = delta <= -s.conf.AnomalyThresholdPct
	case constant.MetricDirectionLowerIsBetter:
		m.Anomaly = delta >= s.conf.AnomalyThresholdPct
	default:
		m.Anomaly = math.Abs(delta) >= s.conf.AnomalyThresholdPct
	}
}

func deltaPct(current, base *float64) *float64 {
	if current == nil || base == nil {
		return nil
	}
	if *base == 0 {
		if *current == 0 {
			return new(0.0)
		}
		return nil // деление на ноль: дельта не определена
	}
	return new(math.Round((*current-*base)/math.Abs(*base)*1000) / 10)
}

func formatValue(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.4g", *v)
}

func humanSince(now, t time.Time) string {
	d := now.Sub(t).Round(time.Minute)
	switch {
	case d < time.Minute:
		return "меньше минуты назад"
	case d < 2*time.Hour:
		return fmt.Sprintf("%d мин назад", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%.1f ч назад", d.Hours())
	default:
		return fmt.Sprintf("%d дн назад", int(d.Hours()/24))
	}
}

// humanDuration — длительность для подсказки: «30 мин», «2 ч», «1.5 ч», «3 дн».
func humanDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	switch {
	case d < time.Minute:
		return "меньше минуты"
	case d < 2*time.Hour && d%time.Hour != 0:
		return fmt.Sprintf("%d мин", int(d.Minutes()))
	case d < 48*time.Hour:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", d.Hours()), ".0") + " ч"
	default:
		return fmt.Sprintf("%d дн", int(d.Hours()/24))
	}
}
