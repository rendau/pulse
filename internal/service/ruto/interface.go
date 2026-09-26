package ruto

import (
	"context"

	rutoModel "github.com/rendau/pulse/internal/service/ruto/model"
)

// Client — read-only доступ к ruto-core (API-gateway): опубликованная конфигурация маршрутов.
type Client interface {
	// GetSnapshot — действующая конфигурация gateway (приложения и маршруты) без секретов:
	// auth, variables, заголовки и query-параметры backend'а не разбираются вовсе.
	GetSnapshot(ctx context.Context) (*rutoModel.Snapshot, error)
	Ping(ctx context.Context) error
	// GatewayErrorsFilter — регэксп строк логов gateway об ошибках (backend не ответил, скрипт
	// трансформации сломан); ParseGatewayError разбирает такую строку.
	GatewayErrorsFilter() string
	ParseGatewayError(line string) (*rutoModel.GatewayError, bool)
}
