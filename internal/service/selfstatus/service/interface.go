package service

import (
	"context"

	svcproxyModel "github.com/rendau/pulse/internal/service/svcproxy/model"
)

// PodGetterI — GET прямо в под (svcproxy или pods/proxy локально).
type PodGetterI interface {
	GetPod(ctx context.Context, target svcproxyModel.PodTarget, path string, query, headers map[string]string, maxBytes int64) (*svcproxyModel.Response, error)
}

// PiiI — персональные данные в тексте — токенами (учётные данные из адресов вырезаются).
type PiiI interface {
	Text(s string) string
}
