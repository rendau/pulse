package snapshot

import (
	"fmt"
	"strings"
	"time"

	"github.com/mechta-market/pulse/internal/constant"
	eventModel "github.com/mechta-market/pulse/internal/domain/event/model"
	snapshotModel "github.com/mechta-market/pulse/internal/domain/snapshot/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
)

// podsState сводит поды workload'а к счётчикам и проблемам; из последних завершений
// контейнеров за окно выводятся события restart / oom_kill.
func podsState(pods []k8sModel.Pod, serviceName string, now time.Time, win time.Duration) (snapshotModel.PodsState, []eventModel.Event) {
	state := snapshotModel.PodsState{Total: len(pods)}
	events := make([]eventModel.Event, 0)
	since := now.Add(-win)

	for _, pod := range pods {
		if pod.Ready {
			state.Ready++
		}
		state.Restarts += pod.Restarts
		if pod.StartedAt.After(state.NewestStartedAt) {
			state.NewestStartedAt = pod.StartedAt
		}

		if pod.Phase == "Pending" || pod.Phase == "Failed" || pod.Phase == "Unknown" {
			state.Problems = append(state.Problems, snapshotModel.PodProblem{Pod: pod.Name, Reason: pod.Phase, At: pod.StartedAt})
		}

		for _, c := range pod.Containers {
			if c.State != "running" && c.Reason != "" && c.Reason != "Completed" {
				state.Problems = append(state.Problems, snapshotModel.PodProblem{
					Pod: pod.Name, Container: c.Name, Reason: c.Reason, At: c.LastTerminatedAt,
				})
			}

			if c.LastTerminationReason == "" || c.LastTerminatedAt.Before(since) {
				continue
			}
			event := eventModel.Event{
				TS:       c.LastTerminatedAt,
				Source:   constant.SourceK8s,
				Service:  serviceName,
				Type:     constant.EventTypeRestart,
				Severity: constant.SeverityWarning,
				Summary:  fmt.Sprintf("%s: контейнер %s/%s перезапущен (%s), всего рестартов %d", serviceName, pod.Name, c.Name, c.LastTerminationReason, c.Restarts),
				Details:  map[string]any{"pod": pod.Name, "container": c.Name, "reason": c.LastTerminationReason, "restarts": c.Restarts},
			}
			if c.LastTerminationReason == "OOMKilled" {
				event.Type = constant.EventTypeOOMKill
				event.Severity = constant.SeverityCritical
				event.Summary = fmt.Sprintf("%s: контейнер %s/%s убит по OOM, всего рестартов %d", serviceName, pod.Name, c.Name, c.Restarts)
			}
			events = append(events, event)
		}
	}

	return state, events
}

// convertK8sEvent переводит событие кластера в нормализованное. Normal-события, кроме
// масштабирования, отбрасываются: они шум для диагностики.
func convertK8sEvent(e k8sModel.Event, serviceName string) (eventModel.Event, bool) {
	event := eventModel.Event{
		TS:      e.LastTS,
		Source:  constant.SourceK8s,
		Service: serviceName,
		Details: map[string]any{"object": e.ObjectKind + "/" + e.ObjectName, "reason": e.Reason, "count": e.Count},
	}

	message := strings.TrimSpace(e.Message)
	summary := fmt.Sprintf("%s: %s %s: %s", serviceName, e.ObjectKind, e.ObjectName, e.Reason)
	if message != "" {
		summary += " — " + message
	}
	if e.Count > 1 {
		summary += fmt.Sprintf(" (×%d)", e.Count)
	}
	event.Summary = summary

	switch {
	case e.Reason == "OOMKilling" || strings.Contains(message, "OOMKilled"):
		event.Type, event.Severity = constant.EventTypeOOMKill, constant.SeverityCritical
	case e.Reason == "ScalingReplicaSet" || e.Reason == "SuccessfulRescale":
		event.Type, event.Severity = constant.EventTypeScale, constant.SeverityInfo
	case e.Reason == "BackOff" && strings.Contains(message, "restarting"):
		event.Type, event.Severity = constant.EventTypeRestart, constant.SeverityWarning
	case e.Type == "Warning":
		event.Type, event.Severity = constant.EventTypeWarning, constant.SeverityWarning
		if e.Reason == "FailedScheduling" || e.Reason == "ImagePullBackOff" || e.Reason == "ErrImagePull" || e.Reason == "Failed" {
			event.Severity = constant.SeverityCritical
		}
	default:
		return eventModel.Event{}, false
	}

	return event, true
}
