// Package service — клиент pulse-agent: находки (POST /v1/findings) с ключом клиента pulse.
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rendau/pulse/internal/errs"
	"github.com/rendau/pulse/internal/infra/httpx"
	pulseagentModel "github.com/rendau/pulse/internal/service/pulseagent/model"
)

const (
	findingsPath = "/v1/findings"
	maxBodyBytes = 64 << 10
)

type Service struct {
	baseUrl    string
	key        string
	httpClient *http.Client
}

// New — клиент pulse-agent (PULSE_AGENT_URL, http://pulse-agent.<namespace>) с ключом PULSE_AGENT_KEY.
func New(baseUrl, key string) *Service {
	return &Service{
		baseUrl:    strings.TrimRight(baseUrl, "/"),
		key:        key,
		httpClient: httpx.New(httpx.Config{Timeout: 10 * time.Second}),
	}
}

func (s *Service) SendFinding(ctx context.Context, finding *pulseagentModel.Finding) (*pulseagentModel.FindingResult, error) {
	rep := &findingRep{}
	if err := s.sendRequest(ctx, http.MethodPost, findingsPath, encodeFindingReq(finding), rep); err != nil {
		return nil, err
	}
	return &pulseagentModel.FindingResult{Id: rep.Id, Deduplicated: rep.Deduplicated}, nil
}

// sendRequest — единственная точка отправки запросов: JSON-тело, ключ клиента, статус,
// десериализация ответа.
func (s *Service) sendRequest(ctx context.Context, method, path string, reqObj, repObj any) error {
	target := s.baseUrl + path

	raw, err := json.Marshal(reqObj)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.key)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}
		return fmt.Errorf("%w: %s: %w", errs.ServiceNA, target, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s", pulseagentModel.ErrRateLimited, target)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return fmt.Errorf("%w: %s: status %d: %s", errs.ServiceNA, target, resp.StatusCode, truncate(body, 200))
	}

	if err = json.Unmarshal(body, repObj); err != nil {
		return fmt.Errorf("decode %s: %w", target, err)
	}
	return nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// транспортные модели

type findingReq struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Text     string `json:"text"`
	Source   string `json:"source"`
}

func encodeFindingReq(f *pulseagentModel.Finding) findingReq {
	return findingReq{Key: f.Key, Kind: f.Kind, Severity: f.Severity, Title: f.Title, Text: f.Text, Source: f.Source}
}

type findingRep struct {
	Id           int64 `json:"id"`
	Deduplicated bool  `json:"deduplicated"`
}
