// Package service — нормализация фактов из источников в Event (ТЗ 1.2). Чистые функции:
// одни и те же правила в снапшоте и таймлайне.
package service

import (
	"fmt"
	"strings"

	"github.com/samber/lo"

	"github.com/mechta-market/pulse/internal/constant"
	deployModel "github.com/mechta-market/pulse/internal/domain/deploy/model"
	"github.com/mechta-market/pulse/internal/domain/event/model"
)

type Service struct{}

func New() *Service {
	return &Service{}
}

// FromCluster переводит событие кластера в нормализованное. Normal-события, кроме
// масштабирования, отбрасываются: они шум для диагностики.
func (s *Service) FromCluster(e model.ClusterEvent, service string) (model.Event, bool) {
	event := model.Event{
		TS:      e.TS,
		Source:  constant.SourceK8s,
		Service: service,
		Details: map[string]any{"object": e.ObjectKind + "/" + e.ObjectName, "reason": e.Reason, "count": e.Count},
	}

	message := strings.TrimSpace(e.Message)
	summary := fmt.Sprintf("%s: %s %s: %s", service, e.ObjectKind, e.ObjectName, e.Reason)
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
		return model.Event{}, false
	}

	return event, true
}

// FromTermination — рестарт или OOM из последнего завершения контейнера.
func (s *Service) FromTermination(t model.ContainerTermination, service string) (model.Event, bool) {
	if t.Reason == "" || t.Reason == "Completed" {
		return model.Event{}, false
	}

	event := model.Event{
		TS:       t.At,
		Source:   constant.SourceK8s,
		Service:  service,
		Type:     constant.EventTypeRestart,
		Severity: constant.SeverityWarning,
		Summary:  fmt.Sprintf("%s: контейнер %s/%s перезапущен (%s), всего рестартов %d", service, t.Pod, t.Container, t.Reason, t.Restarts),
		Details:  map[string]any{"pod": t.Pod, "container": t.Container, "reason": t.Reason, "restarts": t.Restarts},
	}
	if t.Reason == "OOMKilled" {
		event.Type = constant.EventTypeOOMKill
		event.Severity = constant.SeverityCritical
		event.Summary = fmt.Sprintf("%s: контейнер %s/%s убит по OOM, всего рестартов %d", service, t.Pod, t.Container, t.Restarts)
	}

	return event, true
}

// FromDeploy — событие деплоя из истории индексера.
func (s *Service) FromDeploy(d *deployModel.Main) model.Event {
	from := lo.CoalesceOrEmpty(short(d.PrevCommit), short(digestTail(d.PrevImageDigest)), d.PrevImage, "?")
	to := lo.CoalesceOrEmpty(short(d.DeployedCommit), short(digestTail(d.ImageDigest)), d.Image, "?")

	return model.Event{
		TS:       d.ObservedAt,
		Source:   constant.SourceK8s,
		Type:     constant.EventTypeDeploy,
		Service:  d.ServiceName,
		Severity: constant.SeverityInfo,
		Summary:  fmt.Sprintf("%s: деплой %s → %s (%s %s/%s)", d.ServiceName, from, to, d.Kind, d.Namespace, d.Name),
		Details: map[string]any{
			"workload": d.Kind + "/" + d.Namespace + "/" + d.Name,
			"image":    d.Image, "image_digest": d.ImageDigest, "commit": d.DeployedCommit,
			"prev_image": d.PrevImage, "prev_image_digest": d.PrevImageDigest, "prev_commit": d.PrevCommit,
		},
	}
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func digestTail(digest string) string {
	_, hex, ok := strings.Cut(digest, ":")
	if !ok {
		return digest
	}
	return hex
}
