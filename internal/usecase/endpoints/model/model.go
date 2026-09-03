package model

import "time"

// CallReq — вызов диагностической ручки: только id из декларации и объявленные параметры.
type CallReq struct {
	Service    string
	EndpointId string
	Params     map[string]any
}

// CallResult — ответ ручки после лимитов и маскирования PII.
type CallResult struct {
	Service    string
	EndpointId string
	Title      string
	Url        string // без query; для отладки, куда ходили
	StatusCode int
	Duration   time.Duration
	// Data — разобранный JSON (объект/массив/значение) либо строка для не-JSON
	Data       any
	Rows       int
	TotalRows  int
	Truncated  bool
	MaskedKeys []string
}
