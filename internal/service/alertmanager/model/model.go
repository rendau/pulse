package model

import "time"

// Alert — алерт из Alertmanager API v2.
type Alert struct {
	Fingerprint string
	Labels      map[string]string
	Annotations map[string]string
	StartsAt    time.Time
	EndsAt      time.Time
	// State — active | suppressed | unprocessed
	State string
}

func (a Alert) Name() string     { return a.Labels["alertname"] }
func (a Alert) Severity() string { return a.Labels["severity"] }
