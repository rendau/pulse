package model

import "time"

// CallReq — вызов диагностической ручки: только id из манифеста и объявленные параметры.
type CallReq struct {
	Service    string
	EndpointId string
	Params     map[string]any
}

// CallResult — ответ ручки после проекции на схему манифеста, токенов и лимитов.
type CallResult struct {
	Service    string
	EndpointId string
	Title      string
	// RequestId — X-Pulse-Request-Id вызова: по нему вызов находится в логах сервиса
	RequestId  string
	StatusCode int
	Duration   time.Duration
	// Data — ответ, спроецированный на схему (только объявленные поля); ошибка ручки — {"error": …}
	Data      any
	Rows      int
	TotalRows int
	Truncated bool
	// PersonalFields — поля, чьи значения заменены токенами pii:…
	PersonalFields []string
	// DroppedFields — сколько полей ответа вырезано: их нет в схеме манифеста
	DroppedFields int
}
