package snapshot

import (
	"time"

	eventModel "github.com/rendau/pulse/internal/domain/event/model"
	snapshotModel "github.com/rendau/pulse/internal/domain/snapshot/model"
	k8sModel "github.com/rendau/pulse/internal/service/k8s/model"
)

// podsState сводит поды workload'а к счётчикам и проблемам; из последних завершений
// контейнеров за окно выводятся события restart / oom_kill.
func podsState(events_ eventServiceI, pods []k8sModel.Pod, serviceName string, now time.Time, win time.Duration) (snapshotModel.PodsState, []eventModel.Event) {
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
			if event, ok := events_.FromTermination(eventModel.ContainerTermination{
				At: c.LastTerminatedAt, Pod: pod.Name, Container: c.Name, Reason: c.LastTerminationReason, Restarts: c.Restarts,
			}, serviceName); ok {
				events = append(events, event)
			}
		}
	}

	return state, events
}
