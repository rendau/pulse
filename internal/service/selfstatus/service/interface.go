package service

import (
	"context"

	svcproxyModel "github.com/rendau/pulse/internal/service/svcproxy/model"
)

// ServiceGetterI — GET в k8s Service (svcproxy или services/proxy локально).
type ServiceGetterI interface {
	GetService(ctx context.Context, target svcproxyModel.ServiceTarget, path string, query, headers map[string]string, maxBytes int64) (*svcproxyModel.Response, error)
}

// PiiI — персональные данные в тексте — токенами (учётные данные из адресов вырезаются).
type PiiI interface {
	Text(s string) string
}
