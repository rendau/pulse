package endpoints

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	svcproxyModel "github.com/mechta-market/pulse/internal/service/svcproxy/model"
	"github.com/mechta-market/pulse/internal/usecase/endpoints/model"
)

type fakeSvc struct{ service *svcModel.Main }

func (f fakeSvc) GetOrSuggest(context.Context, string) (*svcModel.Main, error) { return f.service, nil }

type fakeWorkload struct{}

func (fakeWorkload) List(context.Context, *workloadModel.ListReq) ([]*workloadModel.Main, int64, error) {
	return []*workloadModel.Main{{Namespace: "prod", Kind: "Deployment", Name: "payments-api", ServiceName: "payments-api"}}, 1, nil
}

type fakeCaller struct {
	namespace, service, path string
	port                     int
	query                    map[string]string
	body                     string
	err                      error
}

func (f *fakeCaller) Get(_ context.Context, namespace, service string, port int, path string, query map[string]string, maxBytes int64) (*svcproxyModel.Response, error) {
	f.namespace, f.service, f.port, f.path, f.query = namespace, service, port, path, query
	if f.err != nil {
		return nil, f.err
	}
	body := []byte(f.body)
	resp := &svcproxyModel.Response{StatusCode: 200, ContentType: "application/json", Body: body}
	if int64(len(body)) > maxBytes {
		resp.Body, resp.Truncated = body[:maxBytes], true
	}
	return resp, nil
}

func service() *svcModel.Main {
	s := &svcModel.Main{Name: "payments-api"}
	s.Metadata.Endpoints = []svcModel.Endpoint{
		{
			Id: "stuck_queue_items", Title: "Зависшие записи", Path: "/internal/diagnostics/stuck", Method: "GET",
			Params:  map[string]svcModel.EndpointParam{"older_than_minutes": {Type: "int", Default: "30", Max: new(1440.0)}},
			MaxRows: 2, PII: []string{"customer_name", "phone"}, Timeout: 5 * time.Second,
		},
		{
			Id: "order_details", Path: "/internal/diagnostics/order/{id}", Method: "GET",
			Params: map[string]svcModel.EndpointParam{"id": {Type: "string"}},
			PII:    []string{"customer_name", "phone", "address", "email"},
		},
		{Id: "reset_queue", Path: "/internal/diagnostics/reset", Method: "POST"},
	}
	return s
}

func newUsecase(caller *fakeCaller) *Usecase {
	return New(Config{MaxRows: 100, MaxBodyBytes: 1 << 20, MaxTimeout: 10 * time.Second, DefaultPort: 80}, fakeSvc{service()}, fakeWorkload{}, caller)
}

func TestCall_AllowlistAndValidation(t *testing.T) {
	ctx := context.Background()
	caller := &fakeCaller{body: `[]`}
	u := newUsecase(caller)

	_, err := u.Call(ctx, &model.CallReq{Service: "payments-api", EndpointId: "../admin"})
	assert.ErrorContains(t, err, "unknown endpoint_id", "произвольный путь невозможен")

	_, err = u.Call(ctx, &model.CallReq{Service: "payments-api", EndpointId: "reset_queue"})
	assert.ErrorContains(t, err, "only GET is allowed", "необъявленный метод отклоняется")

	_, err = u.Call(ctx, &model.CallReq{Service: "payments-api", EndpointId: "stuck_queue_items", Params: map[string]any{"limit": 5}})
	assert.ErrorContains(t, err, "not declared")

	_, err = u.Call(ctx, &model.CallReq{Service: "payments-api", EndpointId: "stuck_queue_items", Params: map[string]any{"older_than_minutes": 100000}})
	assert.ErrorContains(t, err, "<= 1440")

	_, err = u.Call(ctx, &model.CallReq{Service: "payments-api", EndpointId: "stuck_queue_items", Params: map[string]any{"older_than_minutes": "ten"}})
	assert.ErrorContains(t, err, "must be a number")

	_, err = u.Call(ctx, &model.CallReq{Service: "payments-api", EndpointId: "order_details"})
	assert.ErrorContains(t, err, "required", "параметр пути обязателен")

	assert.Empty(t, caller.path, "до валидации запрос не уходит")
}

func TestCall_PathParamsAndDefaults(t *testing.T) {
	ctx := context.Background()
	caller := &fakeCaller{body: `{"id":"a/b","customer_name":"Иван","phone":"+7","items":[{"sku":"x","email":"e@x"}]}`}
	u := newUsecase(caller)

	res, err := u.Call(ctx, &model.CallReq{Service: "payments-api", EndpointId: "order_details", Params: map[string]any{"id": "a/b"}})
	require.NoError(t, err)
	assert.Equal(t, "/internal/diagnostics/order/a%2Fb", caller.path, "параметр пути экранирован")
	assert.Empty(t, caller.query)
	assert.Equal(t, "prod", caller.namespace)
	assert.Equal(t, "payments-api", caller.service)
	assert.Equal(t, 80, caller.port)

	// критерий: поля из pii отсутствуют в ответе
	raw, _ := json.Marshal(res.Data)
	assert.NotContains(t, string(raw), "Иван")
	assert.NotContains(t, string(raw), "+7")
	assert.NotContains(t, string(raw), "e@x")
	assert.Contains(t, string(raw), `"sku":"x"`)
	assert.Equal(t, []string{"customer_name", "phone", "address", "email"}, res.MaskedKeys)

	caller.body = `{"items":[{"n":1},{"n":2},{"n":3}]}`
	res, err = u.Call(ctx, &model.CallReq{Service: "payments-api", EndpointId: "stuck_queue_items"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"older_than_minutes": "30"}, caller.query, "дефолт подставлен")
	assert.True(t, res.Truncated)
	assert.Equal(t, 2, res.Rows)
	assert.Equal(t, 3, res.TotalRows)
}

func TestCall_Unreachable(t *testing.T) {
	caller := &fakeCaller{err: errors.New("dial tcp: connection refused")}
	u := newUsecase(caller)

	_, err := u.Call(context.Background(), &model.CallReq{Service: "payments-api", EndpointId: "stuck_queue_items"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "is unreachable")
	assert.ErrorContains(t, err, "payments-api.prod:80")
}
