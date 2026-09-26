package model

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testdata/manifest.json — пример из docs/service-manifest.md: пример стандарта обязан проходить.
func TestParseManifest_DocExample(t *testing.T) {
	raw, err := os.ReadFile("testdata/manifest.json")
	require.NoError(t, err)

	m, err := ParseManifest(raw)
	require.NoError(t, err)
	assert.Empty(t, m.Problems)

	assert.Equal(t, "orders-center", m.Name)
	assert.Equal(t, "Центр заказов", m.Title)
	assert.Equal(t, "orders", m.OwnerTeam)
	assert.Equal(t, []string{"@orders_duty", "orders@company.kz"}, m.OwnerContacts)
	assert.Equal(t, "high", m.Criticality)
	assert.Equal(t, "9f597a7c1e2d4b8a0f3c6e5d7b9a1c2e4f6a8b0c", m.Commit)
	assert.Equal(t, "manifest", m.Metadata.Source)
	require.Len(t, m.Metadata.Dependencies, 3)
	assert.True(t, m.Metadata.Dependencies[0].Critical)
	assert.False(t, m.Metadata.Dependencies[2].Critical)
	require.Len(t, m.Metadata.Metrics, 1)
	require.Len(t, m.Metadata.Logs.ErrorPatterns, 1)

	require.Len(t, m.Metadata.Endpoints, 1)
	e := m.Metadata.Endpoints[0]
	assert.Equal(t, "order_status", e.Id)
	assert.Equal(t, 3*time.Second, e.Timeout)
	assert.Equal(t, 50, e.MaxRows)
	assert.True(t, e.Params["number"].Required, "параметр пути — обязательный")
	assert.Equal(t, "^[0-9]{5,12}$", e.Params["number"].Pattern)
	require.NotNil(t, e.Response)
	assert.Equal(t, "phone", e.Response.Properties["customer_phone"].Personal)
	assert.Equal(t, "array", e.Response.Properties["history"].Type)
	assert.Equal(t, 50, e.Response.Properties["history"].MaxItems)
	assert.Equal(t, "date-time", e.Response.Properties["history"].Items.Properties["at"].Format)
}

func manifest(t *testing.T, patch func(m map[string]any)) []byte {
	t.Helper()
	m := map[string]any{
		"pulse_manifest": 1,
		"service": map[string]any{
			"name": "caravan", "title": "Караван", "description": "Доставка", "criticality": "high",
			"owner": map[string]any{"team": "logistics"},
		},
	}
	patch(m)
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	return raw
}

func endpoint(patch func(e map[string]any)) map[string]any {
	e := map[string]any{
		"id": "order_status", "title": "Заказ", "description": "когда спрашивают о заказе", "path": "/diag/order/{number}",
		"params":   map[string]any{"number": map[string]any{"type": "string", "pattern": "^[0-9]+$"}},
		"response": map[string]any{"type": "object", "properties": map[string]any{"status": map[string]any{"type": "string"}}},
	}
	patch(e)
	return e
}

func TestParseManifest_Invalid(t *testing.T) {
	cases := map[string]func(m map[string]any){
		"нет версии":          func(m map[string]any) { delete(m, "pulse_manifest") },
		"будущая версия":      func(m map[string]any) { m["pulse_manifest"] = 9 },
		"имя":                 func(m map[string]any) { m["service"].(map[string]any)["name"] = "Caravan Service" },
		"нет команды":         func(m map[string]any) { m["service"].(map[string]any)["owner"] = map[string]any{} },
		"критичность":         func(m map[string]any) { m["service"].(map[string]any)["criticality"] = "top" },
		"слишком много ручек": func(m map[string]any) { m["endpoints"] = make([]any, 31) },
	}
	for name, patch := range cases {
		_, err := ParseManifest(manifest(t, patch))
		assert.Error(t, err, name)
	}

	_, err := ParseManifest([]byte("<html>"))
	assert.Error(t, err, "не JSON")
	_, err = ParseManifest(make([]byte, ManifestMaxBytes+1))
	assert.Error(t, err, "больше 64 KB")
}

// Отклоняется часть манифеста — ручка, зависимость, контакт; остальное принято (partial).
func TestParseManifest_Partial(t *testing.T) {
	rejected := map[string]map[string]any{
		"свободная строка": endpoint(func(e map[string]any) {
			e["params"] = map[string]any{"number": map[string]any{"type": "string"}}
		}),
		"секрет в ответе": endpoint(func(e map[string]any) {
			e["id"] = "b"
			e["response"] = map[string]any{"type": "object", "properties": map[string]any{"api_key": map[string]any{"type": "string"}}}
		}),
		"секретный параметр": endpoint(func(e map[string]any) {
			e["id"] = "c"
			e["path"] = "/diag/x"
			e["params"] = map[string]any{"token": map[string]any{"type": "string", "pattern": "^a$"}}
		}),
		"oneOf": endpoint(func(e map[string]any) {
			e["id"] = "d"
			e["response"] = map[string]any{"oneOf": []any{}}
		}),
		"нет схемы ответа": endpoint(func(e map[string]any) {
			e["id"] = "e"
			delete(e, "response")
		}),
		"словарь строк": endpoint(func(e map[string]any) {
			e["id"] = "f"
			e["response"] = map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}
		}),
		"параметр пути не объявлен": endpoint(func(e map[string]any) {
			e["id"] = "g"
			e["params"] = map[string]any{}
		}),
		"поиск по карте": endpoint(func(e map[string]any) {
			e["id"] = "h"
			e["path"] = "/diag/x"
			e["params"] = map[string]any{"pan": map[string]any{"type": "string", "x-personal": "card"}}
		}),
		"неизвестный вид": endpoint(func(e map[string]any) {
			e["id"] = "i"
			e["response"] = map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string", "x-personal": "passport"}}}
		}),
		"rows_path не массив": endpoint(func(e map[string]any) {
			e["id"] = "j"
			e["rows_path"] = "status"
		}),
	}
	accepted := []any{
		endpoint(func(e map[string]any) {
			e["id"] = "orders_by_phone"
			e["description"] = strings.Repeat("д", 1001)
			e["path"] = "/diag/orders"
			e["params"] = map[string]any{"phone": map[string]any{"type": "string", "x-personal": "phone"}}
			e["rows_path"] = "items"
			e["response"] = map[string]any{"type": "object", "properties": map[string]any{
				"items":    map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"number": map[string]any{"type": "string"}}}},
				"by_state": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "integer"}},
			}}
		}),
	}

	raw := manifest(t, func(m map[string]any) {
		m["endpoints"] = append(accepted, anyValues(rejected)...)
		m["service"].(map[string]any)["owner"] = map[string]any{"team": "logistics", "contacts": []any{"@logistics", "+7 701 123 45 67"}}
		m["dependencies"] = []any{
			map[string]any{"id": "pg", "kind": "postgres", "target": "caravan-pg", "critical": true},
			map[string]any{"id": "bank", "kind": "http", "target": "https://user:pass@api.bank.kz"},
			map[string]any{"id": "mq", "kind": "zeromq", "target": "mq"},
		}
		m["build"] = map[string]any{"commit": "latest"}
	})

	m, err := ParseManifest(raw)
	require.NoError(t, err)
	require.Len(t, m.Metadata.Endpoints, 1, m.Problems)
	e := m.Metadata.Endpoints[0]
	assert.Equal(t, "orders_by_phone", e.Id)
	assert.Equal(t, "phone", e.Params["phone"].Personal)
	assert.Equal(t, "integer", e.Response.Properties["by_state"].Values.Type)

	assert.Equal(t, []string{"@logistics"}, m.OwnerContacts, "телефон в контактах не принят")
	require.Len(t, m.Metadata.Dependencies, 1)
	assert.Equal(t, "pg", m.Metadata.Dependencies[0].Id)
	assert.Empty(t, m.Commit)
	assert.Contains(t, m.Problems, "endpoints.orders_by_phone.description: 1001 символов, лимит 1000 — обрезано", "обрезка — не молча")
	// 10 ручек + телефон + 2 зависимости + коммит + длинное описание
	assert.Len(t, m.Problems, len(rejected)+5, m.Problems)
}

func anyValues(m map[string]map[string]any) []any {
	result := make([]any, 0, len(m))
	for _, v := range m {
		result = append(result, v)
	}
	return result
}
