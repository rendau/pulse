package service

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
)

func (s *Service) ListEvents(ctx context.Context, namespace string, since time.Time) ([]k8sModel.Event, error) {
	if s.initErr != nil {
		return nil, s.initErr
	}

	events, err := s.clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("Events.List: %w", err)
	}

	result := make([]k8sModel.Event, 0, len(events.Items))
	for _, e := range events.Items {
		encoded := encodeEvent(e)
		if encoded.LastTS.Before(since) {
			continue
		}
		result = append(result, encoded)
	}

	return result, nil
}

func encodeEvent(e corev1.Event) k8sModel.Event {
	result := k8sModel.Event{
		Namespace:  e.Namespace,
		ObjectKind: e.InvolvedObject.Kind,
		ObjectName: e.InvolvedObject.Name,
		Reason:     e.Reason,
		Type:       e.Type,
		Message:    e.Message,
		Count:      e.Count,
		FirstTS:    e.FirstTimestamp.Time,
		LastTS:     e.LastTimestamp.Time,
	}

	// события через EventSeries/EventTime (новый API) не заполняют Last/FirstTimestamp
	if result.LastTS.IsZero() {
		if e.Series != nil {
			result.LastTS = e.Series.LastObservedTime.Time
			result.Count = e.Series.Count
		} else {
			result.LastTS = e.EventTime.Time
		}
	}
	if result.FirstTS.IsZero() {
		result.FirstTS = e.EventTime.Time
	}
	if result.Count == 0 {
		result.Count = 1
	}

	return result
}
