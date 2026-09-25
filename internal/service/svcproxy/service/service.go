// Package service — прямой вызов ручек сервисов внутри кластера. Общий таймаут не задан:
// дедлайн ставит контекст вызова (из декларации ручки с потолком из правил).
package service

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"

	"github.com/mechta-market/pulse/internal/errs"
	"github.com/mechta-market/pulse/internal/infra/httpx"
	svcproxyModel "github.com/mechta-market/pulse/internal/service/svcproxy/model"
)

type Service struct {
	httpClient *http.Client
	// domainSuffix — суффикс DNS внутри кластера: svc (name.namespace.svc) или svc.cluster.local
	domainSuffix string
}

func New(domainSuffix string) *Service {
	if domainSuffix == "" {
		domainSuffix = "svc"
	}
	httpClient := httpx.New(httpx.Config{ResponseHeaderTimeout: 0})
	// редиректам не следуем: ручка отвечает сама, а не отсылает pulse куда-то ещё
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Service{
		httpClient:   httpClient,
		domainSuffix: domainSuffix,
	}
}

func (s *Service) Get(ctx context.Context, namespace, service string, port int, path string, query map[string]string, maxBytes int64) (*svcproxyModel.Response, error) {
	uri := fmt.Sprintf("http://%s.%s.%s:%d%s", service, namespace, s.domainSuffix, port, path)
	if len(query) > 0 {
		values := url.Values{}
		for k, v := range query {
			values.Set(k, v)
		}
		uri += "?" + values.Encode()
	}

	return s.sendRequest(ctx, uri, nil, maxBytes)
}

func (s *Service) GetPod(ctx context.Context, target svcproxyModel.PodTarget, path string, query, headers map[string]string, maxBytes int64) (*svcproxyModel.Response, error) {
	uri := "http://" + net.JoinHostPort(target.IP, strconv.Itoa(target.Port)) + path
	if len(query) > 0 {
		values := url.Values{}
		for k, v := range query {
			values.Set(k, v)
		}
		uri += "?" + values.Encode()
	}

	return s.sendRequest(ctx, uri, headers, maxBytes)
}

// sendRequest — единственная точка отправки: только GET, тело читается с лимитом.
func (s *Service) sendRequest(ctx context.Context, uri string, headers map[string]string, maxBytes int64) (*svcproxyModel.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Accept", "application/json, text/plain;q=0.9, */*;q=0.1")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", errs.ServiceNA, uri, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	result := &svcproxyModel.Response{StatusCode: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Body: body}
	if int64(len(body)) > maxBytes {
		result.Body = body[:maxBytes]
		result.Truncated = true
	}

	return result, nil
}
