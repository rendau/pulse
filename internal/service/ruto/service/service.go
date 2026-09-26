package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"

	"github.com/rendau/pulse/internal/errs"
	"github.com/rendau/pulse/internal/infra/httpx"
	rutoModel "github.com/rendau/pulse/internal/service/ruto/model"
)

const (
	versionPath  = "/api/snapshot/version"
	snapshotPath = "/api/snapshot"
	maxBodyBytes = 32 << 20
	// versionTTL — как часто сверять версию конфигурации; сам снапшот перечитывается только при её смене
	versionTTL = 30 * time.Second
)

// Service — клиент ruto-core. Внутри кластера API снапшота доступен без авторизации.
type Service struct {
	baseUrl    string
	httpClient *http.Client

	mu        sync.Mutex
	cached    *rutoModel.Snapshot
	checkedAt time.Time
}

func New(baseUrl string) *Service {
	return &Service{
		baseUrl:    strings.TrimRight(baseUrl, "/"),
		httpClient: httpx.New(httpx.Config{Timeout: 15 * time.Second}),
	}
}

func (s *Service) Ping(ctx context.Context) error {
	_, err := s.version(ctx)
	return err
}

// GetSnapshot отдаёт снапшот из кэша, пока версия конфигурации в ruto не изменилась.
func (s *Service) GetSnapshot(ctx context.Context) (*rutoModel.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cached != nil && time.Since(s.checkedAt) < versionTTL {
		return s.cached, nil
	}

	version, err := s.version(ctx)
	if err != nil {
		return nil, err
	}
	if s.cached != nil && s.cached.Version == version {
		s.checkedAt = time.Now()
		return s.cached, nil
	}

	rep := &snapshotRep{}
	if err = s.sendRequest(ctx, snapshotPath, rep); err != nil {
		return nil, err
	}

	s.cached = rep.decode(version)
	s.checkedAt = time.Now()
	return s.cached, nil
}

func (s *Service) version(ctx context.Context) (string, error) {
	rep := &versionRep{}
	if err := s.sendRequest(ctx, versionPath, rep); err != nil {
		return "", err
	}
	return rep.Version, nil
}

// sendRequest — единственная точка отправки запросов: GET, проверка статуса, десериализация.
func (s *Service) sendRequest(ctx context.Context, path string, repObj any) error {
	uri := s.baseUrl + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", errs.ServiceNA, uri, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%w: %s: status %d", errs.ServiceNA, uri, resp.StatusCode)
	}

	if err = json.Unmarshal(body, repObj); err != nil {
		return fmt.Errorf("decode %s: %w", uri, err)
	}
	return nil
}

// транспортные модели: только нужные поля. auth, variables, backend.headers/query_params
// содержат секреты — они намеренно не объявлены и не попадают в память сервиса.

type versionRep struct {
	Version string `json:"version"`
}

type snapshotRep struct {
	Data struct {
		BaseUrl string   `json:"base_url"`
		Apps    []appRep `json:"apps"`
	} `json:"data"`
}

type appRep struct {
	Id                 string `json:"id"`
	Name               string `json:"name"`
	Active             bool   `json:"active"`
	PathPrefix         string `json:"path_prefix"`
	ExcludeFromMetrics bool   `json:"exclude_from_metrics"`
	Backend            struct {
		Url     string `json:"url"`
		GrpcUrl string `json:"grpc_url"`
	} `json:"backend"`
	Endpoints []endpointRep `json:"endpoints"`
}

type endpointRep struct {
	Id     string `json:"id"`
	Active bool   `json:"active"`
	Type   string `json:"type"`
	Http   struct {
		Method string `json:"method"`
		Path   string `json:"path"`
	} `json:"http"`
	Grpc struct {
		Path string `json:"path"`
	} `json:"grpc"`
}

func (r *snapshotRep) decode(version string) *rutoModel.Snapshot {
	return &rutoModel.Snapshot{
		Version: version,
		BaseUrl: r.Data.BaseUrl,
		Apps:    lo.Map(r.Data.Apps, decodeApp),
	}
}

func decodeApp(v appRep, _ int) rutoModel.App {
	return rutoModel.App{
		Id:                 v.Id,
		Name:               v.Name,
		Active:             v.Active,
		PathPrefix:         v.PathPrefix,
		BackendUrl:         v.Backend.Url,
		GrpcUrl:            v.Backend.GrpcUrl,
		ExcludeFromMetrics: v.ExcludeFromMetrics,
		Endpoints:          lo.Map(v.Endpoints, decodeEndpoint),
	}
}

func decodeEndpoint(v endpointRep, _ int) rutoModel.Endpoint {
	return rutoModel.Endpoint{
		Id:       v.Id,
		Active:   v.Active,
		Type:     lo.CoalesceOrEmpty(v.Type, rutoModel.EndpointTypeHttp),
		Method:   strings.ToUpper(v.Http.Method),
		Path:     v.Http.Path,
		GrpcPath: v.Grpc.Path,
	}
}

// gatewayErrorsFilter — подстроки сообщений об ошибках gateway (регэксп для логов): backend не
// ответил (proxy error) и скрипт трансформации не компилируется или падает.
const gatewayErrorsFilter = `proxy error |transform: (compile|run) failed`

// GatewayErrorsFilter — регэксп строк логов gateway, которые разбирает ParseGatewayError.
func (s *Service) GatewayErrorsFilter() string {
	return gatewayErrorsFilter
}

// gatewayLogLine — строка лога gateway (JSON slog).
type gatewayLogLine struct {
	Msg        string `json:"msg"`
	Reason     string `json:"reason"`
	Error      string `json:"error"`
	AppName    string `json:"app_name"`
	AppId      string `json:"app_id"`
	EndpointId string `json:"endpoint_id"`
}

// ParseGatewayError — ошибка gateway из строки его лога; не та строка — false. Отмена запроса
// клиентом — не ошибка backend'а.
func (s *Service) ParseGatewayError(line string) (*rutoModel.GatewayError, bool) {
	var v gatewayLogLine
	if json.Unmarshal([]byte(line), &v) != nil {
		return nil, false
	}
	switch {
	case strings.HasPrefix(v.Msg, "proxy error ") && v.AppName != "" && v.Reason != "client canceled request":
		return &rutoModel.GatewayError{
			Kind: rutoModel.GatewayErrorProxy, AppName: v.AppName,
			Reason: lo.CoalesceOrEmpty(v.Reason, "backend request failed"), Error: v.Error,
		}, true
	case strings.Contains(v.Msg, "transform: ") && strings.HasSuffix(v.Msg, " failed") && v.AppId != "":
		return &rutoModel.GatewayError{
			Kind: rutoModel.GatewayErrorScript, AppId: v.AppId, EndpointId: v.EndpointId,
			Reason: v.Msg, Error: v.Error,
		}, true
	}
	return nil, false
}
