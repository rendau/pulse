// Package service — чтение ручки состояния сервиса (<manifest path>/status) прямо с пода.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"

	"github.com/mechta-market/pulse/internal/constant"
	"github.com/mechta-market/pulse/internal/errs"
	selfstatusModel "github.com/mechta-market/pulse/internal/service/selfstatus/model"
	svcproxyModel "github.com/mechta-market/pulse/internal/service/svcproxy/model"
)

const (
	maxBodyBytes  = 64 << 10
	maxMessage    = 300
	maxTitle      = 100
	maxGauges     = 20
	maxDependency = 30
)

var (
	idRe     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)
	statuses = []string{selfstatusModel.StatusOk, selfstatusModel.StatusDegraded, selfstatusModel.StatusDown}
)

type Config struct {
	// Path — путь ручки состояния (<путь манифеста>/status)
	Path string
	// CacheTtl — сколько держать ответ пода: снапшоты подряд не нагружают сервис
	CacheTtl time.Duration
}

type Service struct {
	conf Config
	pods PodGetterI
	pii  PiiI

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	status *selfstatusModel.Status
	at     time.Time
}

func New(conf Config, pods PodGetterI, pii PiiI) *Service {
	return &Service{conf: conf, pods: pods, pii: pii, cache: map[string]cached{}}
}

func (s *Service) Get(ctx context.Context, target svcproxyModel.PodTarget) (*selfstatusModel.Status, error) {
	key := fmt.Sprintf("%s/%s:%d", target.Namespace, target.Pod, target.Port)
	s.mu.Lock()
	if c, ok := s.cache[key]; ok && time.Since(c.at) < s.conf.CacheTtl {
		s.mu.Unlock()
		return c.status, nil
	}
	s.mu.Unlock()

	resp, err := s.pods.GetPod(ctx, target, s.conf.Path, nil, map[string]string{
		"User-Agent":         constant.ServiceName + "/" + constant.Version,
		"X-Pulse-Request-Id": "snapshot",
	}, maxBodyBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}

	var status *selfstatusModel.Status
	switch {
	case resp.StatusCode == 404:
		// ручки состояния нет — она необязательна
	case resp.StatusCode != 200:
		return nil, fmt.Errorf("%w: %s: status %d", errs.ServiceNA, key, resp.StatusCode)
	case resp.Truncated:
		return nil, fmt.Errorf("%w: %s: ответ больше %d KB", errs.ServiceNA, key, maxBodyBytes>>10)
	default:
		if status, err = s.parse(resp.Body); err != nil {
			return nil, fmt.Errorf("%w: %s: ответ не по стандарту: %w", errs.ServiceNA, key, err)
		}
	}

	s.mu.Lock()
	s.cache[key] = cached{status: status, at: time.Now()}
	for k, c := range s.cache {
		if time.Since(c.at) > s.conf.CacheTtl {
			delete(s.cache, k)
		}
	}
	s.mu.Unlock()
	return status, nil
}

// statusRep — ответ ручки состояния (транспортная модель стандарта).
type statusRep struct {
	Status       string `json:"status"`
	CheckedAt    string `json:"checked_at"`
	Dependencies []struct {
		Id        string   `json:"id"`
		Status    string   `json:"status"`
		LatencyMs *float64 `json:"latency_ms"`
		Message   string   `json:"message"`
	} `json:"dependencies"`
	Gauges []struct {
		Id     string          `json:"id"`
		Title  string          `json:"title"`
		Value  json.RawMessage `json:"value"`
		Unit   string          `json:"unit"`
		Status string          `json:"status"`
	} `json:"gauges"`
}

// parse проверяет ответ: неизвестный итоговый статус — ошибка; записи не по стандарту
// пропускаются; тексты очищаются (учётные данные в адресах, телефоны, email, карты).
func (s *Service) parse(body []byte) (*selfstatusModel.Status, error) {
	rep := &statusRep{}
	if err := json.Unmarshal(body, rep); err != nil {
		return nil, fmt.Errorf("не JSON: %w", err)
	}
	if !slices.Contains(statuses, rep.Status) {
		return nil, fmt.Errorf("status %q: ожидается ok, degraded или down", rep.Status)
	}

	result := &selfstatusModel.Status{Status: rep.Status}
	if t, err := time.Parse(time.RFC3339, rep.CheckedAt); err == nil {
		result.CheckedAt = t
	}

	for _, d := range lo.Slice(rep.Dependencies, 0, maxDependency) {
		if !idRe.MatchString(d.Id) || !slices.Contains(statuses, d.Status) {
			continue
		}
		dep := selfstatusModel.Dependency{Id: d.Id, Status: d.Status}
		if d.LatencyMs != nil && *d.LatencyMs >= 0 {
			dep.LatencyMs = new(int64(*d.LatencyMs))
		}
		if d.Status != selfstatusModel.StatusOk {
			dep.Message = s.cleanText(d.Message, maxMessage)
		}
		result.Dependencies = append(result.Dependencies, dep)
	}

	for _, g := range lo.Slice(rep.Gauges, 0, maxGauges) {
		if !idRe.MatchString(g.Id) {
			continue
		}
		gauge := selfstatusModel.Gauge{Id: g.Id, Title: s.cleanText(g.Title, maxTitle), Unit: s.cleanText(g.Unit, 20)}
		if slices.Contains(statuses, g.Status) {
			gauge.Status = g.Status
		}
		var num float64
		var str string
		switch {
		case json.Unmarshal(g.Value, &num) == nil:
			gauge.Value = &num
		case json.Unmarshal(g.Value, &str) == nil:
			t, err := time.Parse(time.RFC3339, str)
			if err != nil {
				continue // значение — только число или время: свободный текст не принимается
			}
			gauge.Time = &t
		default:
			continue
		}
		result.Gauges = append(result.Gauges, gauge)
	}

	return result, nil
}

// cleanText — текст от сервиса: без учётных данных в адресах, персональные данные — токенами,
// по длине.
func (s *Service) cleanText(text string, limit int) string {
	text = s.pii.Text(strings.TrimSpace(text))
	if r := []rune(text); len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return text
}
