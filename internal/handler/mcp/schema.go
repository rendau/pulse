package mcp

import (
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// addTool — mcp.AddTool со схемой ответа, допускающей новые поля. MCP-клиенты запоминают
// схему при подключении и проверяют по ней structuredContent: со строгой схемой
// (additionalProperties: false, так её выводит SDK) каждый деплой, добавивший поле в ответ,
// ломал бы уже открытые сессии до переподключения. Добавлять поля можно; удалять и
// переименовывать — по-прежнему нельзя. Схема входа остаётся строгой.
func addTool[In, Out any](server *mcp.Server, tool *mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) {
	tool.OutputSchema = outputSchema[Out]()
	mcp.AddTool(server, tool, handler)
}

func outputSchema[Out any]() *jsonschema.Schema {
	schema, err := jsonschema.For[Out](nil)
	if err != nil {
		panic(fmt.Errorf("output schema %v: %w", reflect.TypeFor[Out](), err))
	}
	allowAdditionalProperties(schema)
	return schema
}

// allowAdditionalProperties убирает запрет лишних полей (false-схему в additionalProperties)
// во всём дереве; additionalProperties словарей (схема значения) не трогает.
func allowAdditionalProperties(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	if isFalseSchema(s.AdditionalProperties) {
		s.AdditionalProperties = nil
	}

	children := []*jsonschema.Schema{s.Items, s.AdditionalProperties, s.AdditionalItems, s.Not}
	children = append(children, s.PrefixItems...)
	children = append(children, s.AllOf...)
	children = append(children, s.AnyOf...)
	children = append(children, s.OneOf...)
	for _, m := range []map[string]*jsonschema.Schema{s.Properties, s.PatternProperties, s.Defs, s.Definitions} {
		for _, child := range m {
			children = append(children, child)
		}
	}
	for _, child := range children {
		allowAdditionalProperties(child)
	}
}

func isFalseSchema(s *jsonschema.Schema) bool {
	return s != nil && reflect.DeepEqual(*s, jsonschema.Schema{Not: &jsonschema.Schema{}})
}
