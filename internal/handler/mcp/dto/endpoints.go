package dto

import (
	"slices"

	"github.com/samber/lo"

	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	usecaseEndpointsModel "github.com/mechta-market/pulse/internal/usecase/endpoints/model"
)

// декларация ручек в карточке сервиса (get_service_info)

type EndpointDef struct {
	Id          string                   `json:"id"`
	Title       string                   `json:"title,omitempty"`
	Description string                   `json:"description,omitempty" jsonschema:"когда вызывать — от владельца сервиса"`
	Params      map[string]EndpointParam `json:"params,omitempty"`
	Fields      []string                 `json:"fields,omitempty" jsonschema:"поля ответа верхнего уровня; (personal: вид) — значение придёт токеном pii:…"`
	MaxRows     int                      `json:"max_rows,omitempty"`
	PII         []string                 `json:"pii,omitempty" jsonschema:"поля ответа с персональными данными — придут токенами pii:…"`
}

type EndpointParam struct {
	Type        string   `json:"type"`
	Default     string   `json:"default,omitempty"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Pattern     string   `json:"pattern,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Description string   `json:"description,omitempty"`
	Personal    string   `json:"personal,omitempty" jsonschema:"вид персональных данных: передавай токен pii:… из ответов pulse"`
}

func EncodeEndpointDef(v svcModel.Endpoint, _ int) EndpointDef {
	return EndpointDef{
		Id:          v.Id,
		Title:       v.Title,
		Description: v.Description,
		Params: lo.MapValues(v.Params, func(p svcModel.EndpointParam, _ string) EndpointParam {
			return EndpointParam{
				Type: p.Type, Default: p.Default, Min: p.Min, Max: p.Max, Required: p.Required,
				Pattern: p.Pattern, Enum: p.Enum, Description: p.Description, Personal: p.Personal,
			}
		}),
		Fields:  responseFields(v.Response),
		MaxRows: v.MaxRows,
		PII:     personalFields(v.Response, ""),
	}
}

// personalFields — пути полей схемы с x-personal: их значения придут токенами.
func personalFields(schema *svcModel.Schema, path string) []string {
	if schema == nil {
		return nil
	}
	if schema.Personal != "" {
		return []string{path}
	}
	var result []string
	if schema.Items != nil {
		result = append(result, personalFields(schema.Items, path+"[]")...)
	}
	names := lo.Keys(schema.Properties)
	slices.Sort(names)
	for _, name := range names {
		child := name
		if path != "" {
			child = path + "." + name
		}
		result = append(result, personalFields(schema.Properties[name], child)...)
	}
	return result
}

// responseFields — поля ответа верхнего уровня по схеме: «history[]», «phone (personal: phone)».
func responseFields(schema *svcModel.Schema) []string {
	if schema == nil {
		return nil
	}
	if schema.Type == "array" && schema.Items != nil {
		schema = schema.Items
	}
	names := lo.Keys(schema.Properties)
	slices.Sort(names)
	return lo.Map(names, func(name string, _ int) string {
		field := schema.Properties[name]
		switch {
		case field.Personal != "":
			return name + " (personal: " + field.Personal + ")"
		case field.Type == "array":
			return name + "[]"
		default:
			return name
		}
	})
}

// call_service_endpoint

type CallServiceEndpointReq struct {
	Service    string         `json:"service" jsonschema:"точное имя сервиса"`
	EndpointId string         `json:"endpoint_id" jsonschema:"id ручки из diagnostic_endpoints в get_service_info"`
	Params     map[string]any `json:"params,omitempty" jsonschema:"только объявленные параметры ручки"`
}

type CallServiceEndpointRep struct {
	Service    string   `json:"service"`
	EndpointId string   `json:"endpoint_id"`
	Title      string   `json:"title,omitempty"`
	StatusCode int      `json:"status_code"`
	DurationMs int64    `json:"duration_ms"`
	Data       any      `json:"data" jsonschema:"ответ ручки: только поля из схемы манифеста; персональные данные — токенами pii:…; ошибка ручки — {error}"`
	Rows       int      `json:"rows,omitempty"`
	TotalRows  int      `json:"total_rows,omitempty"`
	Truncated  bool     `json:"truncated" jsonschema:"строк или байт больше лимита — сузь параметры"`
	MaskedKeys []string `json:"masked_keys,omitempty" jsonschema:"поля, чьи значения заменены токенами pii:… (передавай токен дальше как есть: query_logs pattern, персональный параметр ручки)"`
	RequestId  string   `json:"request_id,omitempty" jsonschema:"X-Pulse-Request-Id вызова: по нему вызов находится в логах сервиса"`
	Dropped    int      `json:"dropped_fields,omitempty" jsonschema:"сколько полей ответа вырезано — их нет в схеме манифеста"`
}

func EncodeCallServiceEndpointRep(v *usecaseEndpointsModel.CallResult) CallServiceEndpointRep {
	return CallServiceEndpointRep{
		Service:    v.Service,
		EndpointId: v.EndpointId,
		Title:      v.Title,
		StatusCode: v.StatusCode,
		DurationMs: v.Duration.Milliseconds(),
		Data:       v.Data,
		Rows:       v.Rows,
		TotalRows:  v.TotalRows,
		Truncated:  v.Truncated,
		MaskedKeys: v.PersonalFields,
		RequestId:  v.RequestId,
		Dropped:    v.DroppedFields,
	}
}
