// Package service — клиент kusec по контракту docs/monitoring-api.md проекта kusec.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rendau/pulse/internal/errs"
	"github.com/rendau/pulse/internal/infra/httpx"
	kusecModel "github.com/rendau/pulse/internal/service/kusec/model"
)

const (
	apiPrefix    = "/api"
	maxBodyBytes = 8 << 20
	// pageSize — потолок kusec для list_params.page_size
	pageSize = 100
	// resolveTTL — связь «k8s-объект → приложение» меняется редко
	resolveTTL = 10 * time.Minute
	// syncRunConcurrency — параллельные Get запусков sync (объекты есть только в Get)
	syncRunConcurrency = 5
)

// Auth — API-ключ kusec (ksk_…) со scope read_only.
type Auth struct {
	Token string
}

type Service struct {
	baseUrl    string
	auth       Auth
	httpClient *http.Client

	// resolved — namespace/kube_name → resolvedEntry
	resolved sync.Map
}

type resolvedEntry struct {
	value *kusecModel.Resolved
	at    time.Time
}

// New принимает адрес kusec без /api (http://kusec.kusec).
func New(baseUrl string, auth Auth) *Service {
	return &Service{
		baseUrl:    strings.TrimSuffix(strings.TrimRight(baseUrl, "/"), apiPrefix),
		auth:       auth,
		httpClient: httpx.New(httpx.Config{Timeout: 15 * time.Second}),
	}
}

// Ping проверяет и доступность, и ключ: список приложений требует авторизации.
func (s *Service) Ping(ctx context.Context) error {
	return s.sendRequest(ctx, "/app", url.Values{"list_params.page_size": {"1"}}, nil)
}

// sendRequest — единственная точка отправки запросов: GET, bearer, статус, десериализация.
// Ошибки kusec приходят HTTP 400 с {code, message}.
func (s *Service) sendRequest(ctx context.Context, path string, query url.Values, repObj any) error {
	uri := s.baseUrl + apiPrefix + path
	if len(query) > 0 {
		uri += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	if s.auth.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.auth.Token)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", errs.ServiceNA, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		errRep := errorRep{}
		if json.Unmarshal(body, &errRep) == nil && errRep.Code != "" {
			return fmt.Errorf("%w: %s: status %d: %s %s", errs.ServiceNA, path, resp.StatusCode, errRep.Code, errRep.Message)
		}
		return fmt.Errorf("%w: %s: status %d", errs.ServiceNA, path, resp.StatusCode)
	}

	if repObj != nil {
		if err = json.Unmarshal(body, repObj); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
	}
	return nil
}

type errorRep struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
