package service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/samber/lo"

	alertmanagerModel "github.com/mechta-market/pulse/internal/service/alertmanager/model"
)

const alertsPath = "/api/v2/alerts"

func (s *Service) ListAlerts(ctx context.Context) ([]alertmanagerModel.Alert, error) {
	query := url.Values{"active": {"true"}, "silenced": {"true"}, "inhibited": {"true"}}

	var rep []alertRep
	if _, err := s.sendRequest(ctx, http.MethodGet, alertsPath, query, &rep); err != nil {
		return nil, fmt.Errorf("alerts: %w", err)
	}

	return lo.Map(rep, encodeAlert), nil
}

// transport models

type alertRep struct {
	Fingerprint string            `json:"fingerprint"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    time.Time         `json:"startsAt"`
	EndsAt      time.Time         `json:"endsAt"`
	Status      struct {
		State string `json:"state"`
	} `json:"status"`
}

func encodeAlert(v alertRep, _ int) alertmanagerModel.Alert {
	return alertmanagerModel.Alert{
		Fingerprint: v.Fingerprint,
		Labels:      v.Labels,
		Annotations: v.Annotations,
		StartsAt:    v.StartsAt,
		EndsAt:      v.EndsAt,
		State:       v.Status.State,
	}
}
