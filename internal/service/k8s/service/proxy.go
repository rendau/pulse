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

func (s *Service) ProxyGet(ctx context.Context, namespace, service string, port int, path string, query map[string]string) ([]byte, error) {
	if s.initErr != nil {
		return nil, s.initErr
	}

	body, err := s.clientset.CoreV1().Services(namespace).
		ProxyGet("http", service, strconv.Itoa(port), path, query).
		DoRaw(ctx)
	if err != nil {
		return nil, fmt.Errorf("Services.ProxyGet(%s/%s:%d%s): %w", namespace, service, port, path, err)
	}

	return body, nil
}

// ProxyGetPod выполняет GET к поду через API-сервер (pods/proxy) и отдаёт статус ответа
// пода вместе с телом (404 — не ошибка: так видно, что HTTP-сервер есть, а ручки нет).
// Для локальной разработки, когда IP подов недоступны напрямую; нужен RBAC pods/proxy.
func (s *Service) ProxyGetPod(ctx context.Context, namespace, pod string, port int, path string, query, headers map[string]string, maxBytes int64) (int, []byte, error) {
	if s.initErr != nil {
		return 0, nil, s.initErr
	}

	req := s.clientset.CoreV1().RESTClient().Get().
		Namespace(namespace).Resource("pods").Name(pod + ":" + strconv.Itoa(port)).
		SubResource("proxy").Suffix(path)
	for k, v := range query {
		req = req.Param(k, v)
	}
	for k, v := range headers {
		req = req.SetHeader(k, v)
	}

	body, err := req.Do(ctx).Raw()
	if err != nil {
		// ответ пода не 2xx приходит ошибкой со статусом; порт, который не отвечает, — тоже,
		// но с текстом прокси «error trying to reach service»: это «нет ответа», а не HTTP-ответ
		statusErr, ok := errors.AsType[*apierrors.StatusError](err)
		if !ok || strings.Contains(string(body), "error trying to reach service") {
			return 0, nil, fmt.Errorf("Pods.ProxyGet(%s/%s:%d%s): %w", namespace, pod, port, path, err)
		}
		return int(statusErr.ErrStatus.Code), nil, nil
	}
	if int64(len(body)) > maxBytes {
		body = body[:maxBytes]
	}
	return http.StatusOK, body, nil
}
