// Package service — детерминированные правила здоровья кластера и подсказки (фаза 7.1).
package service

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/rendau/pulse/internal/domain/cluster/model"
	snapshotModel "github.com/rendau/pulse/internal/domain/snapshot/model"
)

// Config — пороги.
type Config struct {
	// PendingPodsThreshold — с этого числа pending-подов кластер считается деградировавшим
	PendingPodsThreshold int
	// ProblemPodsThreshold — с этого числа подов в проблемных состояниях (CrashLoopBackOff,
	// ImagePullBackOff, OOMKilled, Restarting…) кластер считается деградировавшим
	ProblemPodsThreshold int
	// NotReadyDownRatio — доля неготовых нод, с которой кластер считается упавшим
	NotReadyDownRatio float64
	// Public — пороги проблем публичных приложений gateway
	Public PublicConfig
}

type Service struct {
	conf Config
}

func New(conf Config) *Service {
	if conf.PendingPodsThreshold <= 0 {
		conf.PendingPodsThreshold = 5
	}
	if conf.ProblemPodsThreshold <= 0 {
		conf.ProblemPodsThreshold = 3
	}
	if conf.NotReadyDownRatio <= 0 {
		conf.NotReadyDownRatio = 0.5
	}
	conf.Public.defaults()
	return &Service{conf: conf}
}

// ComputeHealth: нет данных о нодах → unknown; неготовых нод ≥ порога или ни одной готовой → down;
// неготовая нода, давление, много pending-подов, много подов в проблемных состояниях,
// critical инфра-алерт или аномалия метрики → degraded.
func (s *Service) ComputeHealth(h *model.Health, nodesUnavailable bool) string {
	if nodesUnavailable || h.Nodes.Total == 0 {
		return snapshotModel.HealthUnknown
	}

	notReady := h.Nodes.Total - h.Nodes.Ready
	if h.Nodes.Ready == 0 || float64(notReady)/float64(h.Nodes.Total) >= s.conf.NotReadyDownRatio {
		return snapshotModel.HealthDown
	}

	if len(h.Nodes.Problems) > 0 ||
		h.Pods.Pending >= s.conf.PendingPodsThreshold ||
		h.Pods.ProblemsTotal >= s.conf.ProblemPodsThreshold {
		return snapshotModel.HealthDegraded
	}
	for _, a := range h.InfraAlerts {
		if a.State == "active" && a.Severity == "critical" {
			return snapshotModel.HealthDegraded
		}
	}
	for _, m := range h.Metrics {
		if m.Anomaly {
			return snapshotModel.HealthDegraded
		}
	}

	return snapshotModel.HealthHealthy
}

// SummaryHints — факты в порядке важности.
func (s *Service) SummaryHints(h *model.Health, now time.Time) []string {
	hints := make([]string, 0, 8)

	if notReady := h.Nodes.Total - h.Nodes.Ready; notReady > 0 {
		hints = append(hints, fmt.Sprintf("нод не готово: %d из %d", notReady, h.Nodes.Total))
	}
	for _, n := range h.Nodes.Problems {
		hints = append(hints, fmt.Sprintf("нода %s: %s", n.Name, strings.Join(n.Problems, ", ")))
	}
	if h.Pods.Pending > 0 {
		hints = append(hints, fmt.Sprintf("pending-подов: %d (порог деградации %d)", h.Pods.Pending, s.conf.PendingPodsThreshold))
	}
	if h.Pods.Failed > 0 {
		hints = append(hints, fmt.Sprintf("failed-подов: %d", h.Pods.Failed))
	}

	byReason := lo.GroupBy(h.Pods.Problems, func(p model.PodProblem) string { return p.Reason })
	reasons := lo.Keys(byReason)
	sort.Slice(reasons, func(i, j int) bool { return len(byReason[reasons[i]]) > len(byReason[reasons[j]]) })
	for _, reason := range reasons {
		problems := byReason[reason]
		services := lo.Uniq(lo.FilterMap(problems, func(p model.PodProblem, _ int) (string, bool) { return p.Service, p.Service != "" }))
		hint := fmt.Sprintf("%d под(ов) в %s", len(problems), reason)
		if len(services) > 0 {
			hint += " (сервисы: " + strings.Join(lo.Slice(services, 0, 5), ", ") + ")"
		}
		hints = append(hints, hint)
	}

	for _, a := range h.InfraAlerts {
		if a.State != "active" {
			continue
		}
		hint := fmt.Sprintf("инфра-алерт %s (%s)", a.Name, lo.CoalesceOrEmpty(a.Severity, "severity не задан"))
		if a.Summary != "" {
			hint += ": " + a.Summary
		}
		hints = append(hints, hint)
	}
	for _, r := range h.SelfReported {
		hint := fmt.Sprintf("%s сообщает о себе: %s", r.Service, r.Report.Status)
		if len(r.Hints) > 0 {
			hint += " — " + strings.TrimPrefix(r.Hints[0], "сервис сообщает: ")
		}
		hints = append(hints, hint)
	}
	for _, app := range h.PublicApps {
		hint := "публичное приложение " + app.App
		if app.Service != "" && app.Service != app.App {
			hint += " (" + app.Service + ")"
		}
		hints = append(hints, hint+": "+strings.Join(lo.Map(app.Problems, func(p model.PublicProblem, _ int) string { return p.Text }), "; "))
	}
	if h.ServiceAlertsActive > 0 {
		hints = append(hints, fmt.Sprintf("активных алертов по сервисам: %d (смотри get_service_snapshot)", h.ServiceAlertsActive))
	}

	for _, m := range h.Metrics {
		if !m.Anomaly || m.DeltaVsYesterday == nil {
			continue
		}
		verb := "вырос"
		if *m.DeltaVsYesterday < 0 {
			verb = "упал"
		}
		hints = append(hints, fmt.Sprintf("%s %s на %.0f%% относительно вчера", m.Id, verb, math.Abs(*m.DeltaVsYesterday)))
	}

	for i, r := range h.EventReasons {
		if i >= 3 {
			break
		}
		hints = append(hints, fmt.Sprintf("события %s: %d за окно в %d namespace(ах)", r.Reason, r.Count, r.Namespaces))
	}

	for _, e := range h.Errors {
		hints = append(hints, e.Hint())
	}

	return hints
}
