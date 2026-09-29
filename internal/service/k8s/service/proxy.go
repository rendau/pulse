package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// ProxyGetService выполняет GET к k8s Service через API-сервер (services/proxy) и отдаёт
// статус ответа вместе с телом (404 — не ошибка: так видно, что HTTP-сервер есть, а ручки нет).
// Для локальной разработки, когда адреса Service недоступны напрямую; нужен RBAC services/proxy.
func (s *Service) ProxyGetService(ctx context.Context, namespace, service string, port int, path string, query, headers map[string]string, maxBytes int64) (int, []byte, error) {
	if s.initErr != nil {
		return 0, nil, s.initErr
	}

	req := s.clientset.CoreV1().RESTClient().Get().
		Namespace(namespace).Resource("services").Name(service + ":" + strconv.Itoa(port)).
		SubResource("proxy").Suffix(path)
	for k, v := range query {
		req = req.Param(k, v)
	}
	for k, v := range headers {
		req = req.SetHeader(k, v)
	}

	body, err := req.Do(ctx).Raw()
	if err != nil {
		// ответ сервиса не 2xx приходит ошибкой со статусом; порт, который не отвечает, и Service
		// без готовых подов — тоже, но с текстом прокси: это «нет ответа», а не HTTP-ответ
		statusErr, ok := errors.AsType[*apierrors.StatusError](err)
		if !ok || strings.Contains(string(body), "error trying to reach service") || strings.Contains(string(body), "no endpoints available") {
			return 0, nil, fmt.Errorf("Services.ProxyGet(%s/%s:%d%s): %w", namespace, service, port, path, err)
		}
		// тело ответа нужно и при ошибке: {"error": "…"} по стандарту манифеста
		if int64(len(body)) > maxBytes {
			body = body[:maxBytes]
		}
		return int(statusErr.ErrStatus.Code), body, nil
	}
	if int64(len(body)) > maxBytes {
		body = body[:maxBytes]
	}
	return http.StatusOK, body, nil
}
