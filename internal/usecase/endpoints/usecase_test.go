package endpoints

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	svcModel "github.com/rendau/pulse/internal/domain/svc/model"
	workloadModel "github.com/rendau/pulse/internal/domain/workload/model"
	"github.com/rendau/pulse/internal/errs"
	piiServiceP "github.com/rendau/pulse/internal/service/pii/service"
	svcproxyModel "github.com/rendau/pulse/internal/service/svcproxy/model"
	"github.com/rendau/pulse/internal/usecase/endpoints/model"
)

type fakeSvc struct{ service *svcModel.Main }

func (f fakeSvc) GetOrSuggest(context.Context, string) (*svcModel.Main, error) { return f.service, nil }

type fakeWorkload struct{}

func (fakeWorkload) List(context.Context, *workloadModel.ListReq) ([]*workloadModel.Main, int64, error) {
	return []*workloadModel.Main{{
		Namespace: "prod", Kind: "Deployment", Name: "ocenter", ServiceName: "orders-center", Selector: "app=ocenter",
		Manifest: workloadModel.Manifest{Status: workloadModel.ManifestOk, Service: "ocenter-api", Port: 3003},
	}}, 1, nil
}

type fakeCaller struct {
	target  svcproxyModel.ServiceTarget
	path    string
	query   map[string]string
	headers map[string]string
	status  int
	body    string
	err     error
}

func (f *fakeCaller) GetService(_ context.Context, target svcproxyModel.ServiceTarget, path string, query, headers map[string]string, maxBytes int64) (*svcproxyModel.Response, error) {
	f.target, f.path, f.query, f.headers = target, path, query, headers
	if f.err != nil {
		return nil, f.err
	}
	body := []byte(f.body)
	resp := &svcproxyModel.Response{StatusCode: f.status, Body: body}
	if int64(len(body)) > maxBytes {
		resp.Body, resp.Truncated = body[:maxBytes], true
	}
	return resp, nil
}

var pii = piiServiceP.New(piiServiceP.Config{})

func str(extra ...func(*svcModel.Schema)) *svcModel.Schema {
	s := &svcModel.Schema{Type: "string"}
	for _, f := range extra {
		f(s)
	}
	return s
}

func service() *svcModel.Main {
	ref := &svcModel.WorkloadRef{Namespace: "prod", Kind: "Deployment", Name: "ocenter"}
	s := &svcModel.Main{Name: "orders-center"}
	s.Metadata.Endpoints = []svcModel.Endpoint{
		{
			Id: "order_status", Title: "Где заказ", Path: "/diag/order/{number}", Workload: ref, Timeout: 3 * time.Second,
			Params: map[string]svcModel.EndpointParam{"number": {Type: "string", Pattern: "[0-9]{5,12}", Required: true}},
			Response: &svcModel.Schema{Type: "object", Properties: map[string]*svcModel.Schema{
				"number":         str(),
				"status":         str(),
				"stuck_reason":   str(func(s *svcModel.Schema) { s.MaxLength = 20 }),
				"customer_phone": str(func(s *svcModel.Schema) { s.Personal = "phone" }),
				"customer_id":    {Type: "integer", Personal: "customer_id"},
				"history": {Type: "array", MaxItems: 2, Items: &svcModel.Schema{Type: "object", Properties: map[string]*svcModel.Schema{
					"status": str(),
				}}},
				"by_state": {Type: "object", Values: &svcModel.Schema{Type: "integer"}},
			}},
		},
		{
			Id: "orders_by_phone", Title: "Заказы клиента", Path: "/diag/orders", Workload: ref, RowsPath: "items", MaxRows: 2,
			Params: map[string]svcModel.EndpointParam{
				"phone": {Type: "string", Personal: "phone", Required: true},
				"state": {Type: "string", Enum: []string{"new", "paid"}},
			},
			Response: &svcModel.Schema{Type: "object", Properties: map[string]*svcModel.Schema{
				"items": {Type: "array", Items: &svcModel.Schema{Type: "object", Properties: map[string]*svcModel.Schema{"number": str()}}},
			}},
		},
		{
			Id: "order_raw", Title: "Заказ как есть", Path: "/diag/order/{number}/raw", Workload: ref, Audience: svcModel.AudienceHuman,
			Params: map[string]svcModel.EndpointParam{"number": {Type: "string", Pattern: "[0-9]{5,12}", Required: true}},
		},
		{Id: "legacy_yaml", Path: "/x"}, // не из манифеста
	}
	return s
}

func newUsecase(caller *fakeCaller) *Usecase {
	return New(Config{MaxRows: 100, MaxBodyBytes: 1 << 20, MaxTimeout: 10 * time.Second}, fakeSvc{service()}, fakeWorkload{}, caller, pii)
}

// Ответ проецируется на схему: необъявленное вырезано, персональные поля — как есть с отметкой
// вида (от модели их прячет агент), строки и массивы — по лимитам.
func TestCall_Projection(t *testing.T) {
	caller := &fakeCaller{status: 200, body: `{
		"number": "234115", "status": "assembling",
		"stuck_reason": "ждём склад, клиент +7 701 123 45 67 звонил",
		"customer_phone": "8 701 123 45 67", "customer_id": 42,
		"customer_name": "Иван Петров", "password": "s3cr3t",
		"history": [{"status": "new", "operator": "Мария"}, {"status": "paid"}, {"status": "assembling"}],
		"by_state": {"new": 1, "paid": "много"}
	}`}
	res, err := newUsecase(caller).Call(context.Background(), &model.CallReq{Service: "orders-center", EndpointId: "order_status", Params: map[string]any{"number": "234115"}})
	require.NoError(t, err)

	assert.Equal(t, svcproxyModel.ServiceTarget{Namespace: "prod", Service: "ocenter-api", Port: 3003}, caller.target, "k8s Service workload'а, его служебный порт")
	assert.Equal(t, "/diag/order/234115", caller.path)
	assert.True(t, strings.HasPrefix(caller.headers["X-Pulse-Request-Id"], "pulse-"))
	assert.Equal(t, caller.headers["X-Pulse-Request-Id"], res.RequestId)

	data := res.Data.(map[string]any)
	assert.Equal(t, "8 701 123 45 67", data["customer_phone"])
	assert.Equal(t, float64(42), data["customer_id"])
	assert.NotContains(t, data, "customer_name", "не объявлено — вырезано")
	assert.NotContains(t, data, "password")
	assert.Equal(t, "ждём склад, клиен...", data["stuck_reason"], "maxLength")
	history := data["history"].([]any)
	require.Len(t, history, 2, "maxItems")
	assert.Equal(t, map[string]any{"status": "new"}, history[0], "operator не объявлен")
	assert.Equal(t, map[string]any{"new": float64(1)}, data["by_state"], "значение словаря не того типа — вырезано")
	assert.Equal(t, map[string]string{"customer_id": "customer_id", "customer_phone": "phone"}, res.PersonalFields)
	assert.Equal(t, 4, res.DroppedFields, "customer_name, password, history[].operator, by_state.paid")
}

func TestCall_Params(t *testing.T) {
	caller := &fakeCaller{status: 200, body: `{"items": [{"number": "1"}, {"number": "2"}, {"number": "3"}]}`}
	u := newUsecase(caller)
	phone := "+7 701 123 45 67"

	res, err := u.Call(context.Background(), &model.CallReq{Service: "orders-center", EndpointId: "orders_by_phone", Params: map[string]any{"phone": phone, "state": "paid"}})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"phone": "77011234567", "state": "paid"}, caller.query, "телефон — к одному виду")
	assert.Equal(t, 2, res.Rows)
	assert.Equal(t, 3, res.TotalRows)
	assert.True(t, res.Truncated)
	assert.Len(t, res.Data.(map[string]any)["items"], 2)

	bad := map[string]map[string]any{
		"неизвестный параметр": {"phone": phone, "debug": true},
		"не из enum": {"phone": phone, "state": "lost"},
		"не телефон": {"phone": "Иванов"},
		"токен":      {"phone": "pii:phone:abcdefghijkl"},
	}
	for name, params := range bad {
		_, err = u.Call(context.Background(), &model.CallReq{Service: "orders-center", EndpointId: "orders_by_phone", Params: params})
		require.ErrorIs(t, err, errs.InvalidRequest, name)
	}

	_, err = u.Call(context.Background(), &model.CallReq{Service: "orders-center", EndpointId: "order_status", Params: map[string]any{"number": "234115; drop"}})
	require.ErrorIs(t, err, errs.InvalidRequest, "pattern — на всё значение")

	_, err = u.Call(context.Background(), &model.CallReq{Service: "orders-center", EndpointId: "unknown"})
	full, ok := errors.AsType[errs.ErrFull](err)
	require.True(t, ok)
	assert.ErrorIs(t, full.Err, errs.ObjectNotFound)
	_, err = u.Call(context.Background(), &model.CallReq{Service: "orders-center", EndpointId: "legacy_yaml"})
	require.ErrorIs(t, err, errs.InvalidConfig, "ручка не из манифеста")
}

func TestCall_ErrorsAndNotJson(t *testing.T) {
	caller := &fakeCaller{status: 404, body: `{"error": "заказ не найден, звоните +7 701 123 45 67", "stack": "at main.go:42"}`}
	u := newUsecase(caller)

	res, err := u.Call(context.Background(), &model.CallReq{Service: "orders-center", EndpointId: "order_status", Params: map[string]any{"number": "99999"}})
	require.NoError(t, err)
	assert.Equal(t, 404, res.StatusCode)
	assert.Equal(t, map[string]any{"error": "заказ не найден, звоните +7 701 123 45 67"}, res.Data, "только текст ошибки, без остального тела")

	caller.status, caller.body = 200, "<html>"
	_, err = u.Call(context.Background(), &model.CallReq{Service: "orders-center", EndpointId: "order_status", Params: map[string]any{"number": "99999"}})
	require.ErrorIs(t, err, errs.ServiceNA, "не JSON — нарушение стандарта")

	caller.err = errors.New("dial tcp 10.96.0.12:3003: connection refused")
	_, err = u.Call(context.Background(), &model.CallReq{Service: "orders-center", EndpointId: "order_status", Params: map[string]any{"number": "99999"}})
	require.ErrorIs(t, err, errs.ServiceNA)
	assert.Contains(t, err.Error(), "prod/ocenter-api:3003")
}

// Ручка для человека: ИИ-клиенту — отказ, агенту — ответ как есть (без проекции на схему), но
// без секретов и карт.
func TestCall_Human(t *testing.T) {
	caller := &fakeCaller{status: 200, body: `{
		"number": "234115", "customer_phone": "+7 701 123 45 67", "comment": "оплата картой 4111 1111 1111 1111",
		"internal": {"api_token": "abc", "retries": 3, "notes": ["позвонить в 18:00"]}
	}`}
	u := newUsecase(caller)

	_, err := u.Call(context.Background(), &model.CallReq{Service: "orders-center", EndpointId: "order_raw", Params: map[string]any{"number": "234115"}})
	full, ok := errors.AsType[errs.ErrFull](err)
	require.True(t, ok)
	assert.ErrorIs(t, full.Err, errs.NoPermission)
	assert.Contains(t, full.Desc, "humans only")
	assert.Empty(t, caller.path, "ИИ-клиенту ручка даже не вызывается")

	result, err := u.Call(context.Background(), &model.CallReq{Service: "orders-center", EndpointId: "order_raw", Params: map[string]any{"number": "234115"}, Human: true})
	require.NoError(t, err)
	assert.Equal(t, "/diag/order/234115/raw", caller.path)
	assert.Equal(t, svcModel.AudienceHuman, result.Audience)
	assert.Equal(t, map[string]any{
		"number": "234115", "customer_phone": "+7 701 123 45 67", "comment": "оплата картой ***1111",
		"internal": map[string]any{"api_token": "***", "retries": float64(3), "notes": []any{"позвонить в 18:00"}},
	}, result.Data)
	assert.Equal(t, 1, result.MaskedFields)
	assert.Zero(t, result.DroppedFields)
	assert.Empty(t, result.PersonalFields)

	// ошибка ручки для человека — тоже с отметкой: агент не отдаст её текст модели
	caller.status, caller.body = 404, `{"error":"заказ 234115 не найден"}`
	result, err = u.Call(context.Background(), &model.CallReq{Service: "orders-center", EndpointId: "order_raw", Params: map[string]any{"number": "234115"}, Human: true})
	require.NoError(t, err)
	assert.Equal(t, svcModel.AudienceHuman, result.Audience)
	assert.Equal(t, map[string]any{"error": "заказ 234115 не найден"}, result.Data)
	caller.status = 200

	// ручка для разбора ИИ — по-прежнему проекция и тому, кому можно ручки для человека
	caller.body = `{"number": "234115", "secret_field": "x"}`
	result, err = u.Call(context.Background(), &model.CallReq{Service: "orders-center", EndpointId: "order_status", Params: map[string]any{"number": "234115"}, Human: true})
	require.NoError(t, err)
	assert.Empty(t, result.Audience)
	assert.Equal(t, 1, result.DroppedFields)
}
