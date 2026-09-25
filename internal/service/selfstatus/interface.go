package selfstatus

import (
	"context"

	selfstatusModel "github.com/mechta-market/pulse/internal/service/selfstatus/model"
	svcproxyModel "github.com/mechta-market/pulse/internal/service/svcproxy/model"
)

// Client — ручка состояния сервиса (docs/service-manifest.md, «Состояние»): что сервис сам
// сообщает о своих зависимостях. Ответ проверен и очищен; кэш на под — несколько секунд.
type Client interface {
	// Get — состояние пода; nil без ошибки — ручки состояния у сервиса нет (404).
	Get(ctx context.Context, target svcproxyModel.PodTarget) (*selfstatusModel.Status, error)
}
