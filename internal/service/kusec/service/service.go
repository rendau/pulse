// Package service — клиент kusec. API чтения истории изменений ещё не согласован
// (ТЗ 9.4–9.5): методы возвращают errs.NotImplemented, чтобы ответы инструментов честно
// показывали отсутствие источника, а не пустой список.
package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mechta-market/pulse/internal/errs"
	"github.com/mechta-market/pulse/internal/infra/httpx"
	kusecModel "github.com/mechta-market/pulse/internal/service/kusec/model"
)

const healthPath = "/healthcheck"

type Auth struct {
	Token string
}

type Service struct {
	baseUrl    string
	auth       Auth
	httpClient *http.Client
}

func New(baseUrl string, auth Auth) *Service {
	return &Service{
		baseUrl:    strings.TrimRight(baseUrl, "/"),
		auth:       auth,
		httpClient: httpx.New(httpx.Config{Timeout: 15 * time.Second}),
	}
}

func (s *Service) Ping(ctx context.Context) error {
	return s.sendRequest(ctx, http.MethodGet, healthPath, nil)
}

func (s *Service) ListChanges(_ context.Context, _ string, _, _ time.Time) ([]kusecModel.Change, error) {
	return nil, fmt.Errorf("%w: kusec change history API is not integrated yet", errs.NotImplemented)
}

// sendRequest — единственная точка отправки запросов.
func (s *Service) sendRequest(ctx context.Context, method, path string, query url.Values) error {
	uri := s.baseUrl + path
	if len(query) > 0 {
		uri += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, uri, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	if s.auth.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.auth.Token)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", errs.ServiceNA, uri, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%w: %s: status %d", errs.ServiceNA, uri, resp.StatusCode)
	}

	return nil
}
