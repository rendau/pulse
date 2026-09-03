package kusec

import (
	"context"
	"time"

	kusecModel "github.com/mechta-market/pulse/internal/service/kusec/model"
)

// Client — read-only доступ к kusec (configmaps, secrets, env). Интеграция уточняется после
// получения проекта kusec (ТЗ 4.1, 9.4–9.5); до этого — контракт и заглушка.
type Client interface {
	// ListChanges — изменения конфигурации сервиса за интервал.
	ListChanges(ctx context.Context, service string, since, until time.Time) ([]kusecModel.Change, error)
	Ping(ctx context.Context) error
}
