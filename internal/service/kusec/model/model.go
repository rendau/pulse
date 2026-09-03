package model

import "time"

// виды конфигурации в kusec
const (
	KindConfigMap = "configmap"
	KindSecret    = "secret"
	KindEnv       = "env"
)

// Change — изменение конфигурации сервиса. Значения секретов сюда не попадают никогда:
// клиент kusec отдаёт для secret только имя ключа и факт изменения.
type Change struct {
	TS        time.Time
	Service   string
	Namespace string
	Kind      string // configmap | secret | env
	Key       string
	OldValue  string
	NewValue  string
	Author    string
}
