package model

import "time"

// CallReq — вызов диагностической ручки: только id из манифеста и объявленные параметры.
type CallReq struct {
	Service    string
	EndpointId string
	Params     map[string]any
	// Human — клиенту можно получить ответ ручки для человека (audience: human): он не отдаёт
	// его модели (агент pulse); остальным такие ручки не вызываются
	Human bool
}

// CallResult — ответ ручки после проекции на схему манифеста и лимитов.
type CallResult struct {
	Service    string
	EndpointId string
	Title      string
	// Audience — svcModel.AudienceHuman: ответ только для человека, как есть (Data без проекции)
	Audience string
	// RequestId — X-Pulse-Request-Id вызова: по нему вызов находится в логах сервиса
	RequestId  string
	StatusCode int
	Duration   time.Duration
	// Data — ответ, спроецированный на схему (только объявленные поля); ошибка ручки — {"error": …}
	Data      any
	Rows      int
	TotalRows int
	Truncated bool
	// PersonalFields — персональные поля ответа: путь (history[].phone) → вид (phone, email…)
	PersonalFields map[string]string
	// DroppedFields — сколько полей ответа вырезано: их нет в схеме манифеста
	DroppedFields int
	// MaskedFields — у ручки для человека: сколько значений полей с именем секрета заменено маской
	MaskedFields int
}
