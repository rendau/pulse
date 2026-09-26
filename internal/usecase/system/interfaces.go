package system

import (
	"context"

	"github.com/rendau/pulse/internal/usecase/system/model"
)

type SystemI interface {
	Ping(ctx context.Context) *model.Ping
}

// Source — источник данных для ping. Ping == nil означает, что источник выключен конфигом.
type Source struct {
	Name string
	Ping func(ctx context.Context) error
}
