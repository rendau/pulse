package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mechta-market/pulse/internal/errs"
	"github.com/mechta-market/pulse/internal/infra/httpx"
)

const (
	readyPath    = "/ready"
	maxBodyBytes = 8 << 20
)

// Auth — авторизация к источнику. Token — bearer; OrgId — X-Scope-OrgID (мультитенантность).
// Basic-auth задаётся через userinfo в URL (https://user:pass@host) и снимается в New.
type Auth struct {
	Token string
	OrgId string
}

type Service struct {
	baseUrl    string
	auth       Auth
	basicUser  string
	basicPass  string
	httpClient *http.Client
}

func New(baseUrl string, auth Auth) *Service {
	s := &Service{
		auth: auth,
		// 30s: счётчик ошибок по логам всего кластера за сутки; поиск назад по суткам —
		// запросами по ≈2 с, его предел — бюджет поиска, а не этот таймаут
		httpClient: httpx.New(httpx.Config{Timeout: 30 * time.Second}),
	}

	baseUrl = strings.TrimRight(baseUrl, "/")
	if u, err := url.Parse(baseUrl); err == nil && u.User != nil {
		s.basicUser = u.User.Username()
		s.basicPass, _ = u.User.Password()
		u.User = nil
		baseUrl = u.String()
	}
	s.baseUrl = baseUrl

	return s
}

func (s *Service) Ping(ctx context.Context) error {
	_, err := s.sendRequest(ctx, http.MethodGet, readyPath, nil, nil)
	return err
}

// sendRequest — единственная точка отправки запросов: собирает URI, добавляет авторизацию,
// проверяет статус, десериализует тело в repObj (если задан) и возвращает сырое тело.
func (s *Service) sendRequest(ctx context.Context, method, path string, query url.Values, repObj any) ([]byte, error) {
	uri := s.baseUrl + path
	if len(query) > 0 {
		uri += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, uri, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}

	switch {
	case s.auth.Token != "":
		req.Header.Set("Authorization", "Bearer "+s.auth.Token)
	case s.basicUser != "":
		req.SetBasicAuth(s.basicUser, s.basicPass)
	}
	if s.auth.OrgId != "" {
		req.Header.Set("X-Scope-OrgID", s.auth.OrgId)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", errs.ServiceNA, uri, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("%w: %s: status %d: %s", errs.ServiceNA, uri, resp.StatusCode, truncate(body, 200))
	}

	if repObj != nil {
		if err = json.Unmarshal(body, repObj); err != nil {
			return nil, fmt.Errorf("decode %s: %w", uri, err)
		}
	}

	return body, nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
