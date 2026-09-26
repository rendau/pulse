package kusec

import (
	"context"

	kusecModel "github.com/rendau/pulse/internal/service/kusec/model"
)

// Client — read-only доступ к kusec (docs/monitoring-api.md проекта kusec): аудит изменений
// конфигурации, журнал sync, сравнение с кластером. Ключ scope=read_only: значения ключей
// kusec не отдаёт, только размер и HMAC-отпечаток; значения обычного конфига в аудите — как есть.
type Client interface {
	// Resolve — приложение kusec по имени k8s-объекта (configmap/secret) в namespace;
	// Found=false — объект kusec не описан. Результат кэшируется.
	Resolve(ctx context.Context, namespace, kubeName string) (*kusecModel.Resolved, error)
	// ListAudit — записи аудита приложения за интервал, новые первыми, не более Limit.
	ListAudit(ctx context.Context, req *kusecModel.AuditReq) ([]kusecModel.AuditEntry, error)
	// ListSyncRuns — запуски sync приложения за интервал с объектами, новые первыми, не более Limit.
	ListSyncRuns(ctx context.Context, req *kusecModel.SyncRunReq) ([]kusecModel.SyncRun, error)
	// GetDrift — расхождение конфигурации приложения в kusec и в кластере (только имена ключей).
	GetDrift(ctx context.Context, appId string) (*kusecModel.Drift, error)
	Ping(ctx context.Context) error
}
