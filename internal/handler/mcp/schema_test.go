package mcp

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type schemaItem struct {
	Name string `json:"name"`
}

type schemaRep struct {
	Items  []schemaItem          `json:"items"`
	One    *schemaItem           `json:"one,omitempty"`
	Counts map[string]int        `json:"counts"`
	Nested map[string]schemaItem `json:"nested"`
}

func TestOutputSchema_AllowsNewFields(t *testing.T) {
	schema := outputSchema[schemaRep]()

	raw, err := json.Marshal(schema)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), `"additionalProperties":false`)

	// схема значения словаря сохраняется
	require.NotNil(t, schema.Properties["counts"].AdditionalProperties)
	assert.Equal(t, "integer", schema.Properties["counts"].AdditionalProperties.Type)

	resolved, err := schema.Resolve(nil)
	require.NoError(t, err)

	// ответ новой версии с полями, которых нет в запомненной клиентом схеме, — валиден
	var newer map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{
		"items": [{"name": "a", "image": "x"}],
		"one": {"name": "b", "since": "2026-09-24"},
		"counts": {"a": 1},
		"nested": {"k": {"name": "c", "extra": true}},
		"services": ["loom"]
	}`), &newer))
	assert.NoError(t, resolved.Validate(newer))

	// типы известных полей по-прежнему проверяются
	var broken map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{"items": [{"name": 1}], "counts": {"a": "x"}, "nested": {}}`), &broken))
	assert.Error(t, resolved.Validate(broken))
}
