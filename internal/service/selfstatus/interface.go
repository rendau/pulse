package selfstatus

import (
	"context"

	selfstatusModel "github.com/rendau/pulse/internal/service/selfstatus/model"
	svcproxyModel "github.com/rendau/pulse/internal/service/svcproxy/model"
)

// Client — ручка состояния сервиса (docs/service-manifest.md, «Состояние»): что сервис сам
// сообщает о своих зависимостях. Ответ проверен и очищен; кэш на Service — несколько секунд.
type Client interface {
	// Get — состояние по словам любого пода за k8s Service; nil без ошибки — ручки состояния у
	// сервиса нет (404).
	Get(ctx context.Context, target svcproxyModel.ServiceTarget) (*selfstatusModel.Status, error)
}
