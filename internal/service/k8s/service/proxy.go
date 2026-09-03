package service

import (
	"context"
	"fmt"
	"strconv"
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
