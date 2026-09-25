package endpoints

import (
	"sort"
	"strconv"
	"strings"

	"github.com/samber/lo"

	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
)

// defaultMaxLength — строка без maxLength в схеме обрезается до этой длины.
const defaultMaxLength = 500

// projector — ответ ручки, спроецированный на схему манифеста (docs/service-manifest.md,
// «Конфиденциальность»): к агенту доходят только объявленные поля нужного типа; персональные —
// токенами; телефоны и email в любом тексте — тоже токенами; строки — по длине.
type projector struct {
	pii      PiiI
	dropped  int
	personal []string
}

func (p *projector) value(v any, schema *svcModel.Schema, path string) any {
	if v == nil {
		return nil
	}
	switch schema.Type {
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			return p.drop()
		}
		result := make(map[string]any, len(schema.Properties))
		keys := lo.Keys(obj)
		sort.Strings(keys)
		for _, key := range keys {
			child, declared := schema.Properties[key]
			switch {
			case declared:
				if value := p.value(obj[key], child, join(path, key)); value != nil || obj[key] == nil {
					result[key] = value
				}
			case schema.Values != nil:
				// словарь: ключи — текст (через токены), значения — только числа/булевы
				if value := p.value(obj[key], schema.Values, join(path, "{}")); value != nil {
					result[p.pii.Text(key)] = value
				}
			default:
				p.dropped++
			}
		}
		return result
	case "array":
		arr, ok := v.([]any)
		if !ok {
			return p.drop()
		}
		if schema.MaxItems > 0 && len(arr) > schema.MaxItems {
			arr = arr[:schema.MaxItems]
		}
		result := make([]any, 0, len(arr))
		for _, item := range arr {
			if value := p.value(item, schema.Items, path+"[]"); value != nil {
				result = append(result, value)
			}
		}
		return result
	case "string":
		s, ok := v.(string)
		if !ok {
			return p.drop()
		}
		if schema.Personal != "" {
			p.personal = append(p.personal, path)
			return p.pii.Tokenize(schema.Personal, s)
		}
		return lo.Ellipsis(p.pii.Text(s), lo.CoalesceOrEmpty(schema.MaxLength, defaultMaxLength))
	case "integer", "number":
		num, ok := v.(float64)
		if !ok || (schema.Type == "integer" && num != float64(int64(num))) {
			return p.drop()
		}
		if schema.Personal != "" {
			p.personal = append(p.personal, path)
			return p.pii.Tokenize(schema.Personal, strconv.FormatFloat(num, 'f', -1, 64))
		}
		return num
	case "boolean":
		b, ok := v.(bool)
		if !ok {
			return p.drop()
		}
		return b
	default:
		return p.drop()
	}
}

// drop — значение не того типа, что в схеме: не пропускается.
func (p *projector) drop() any {
	p.dropped++
	return nil
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// limitRows режет список (rows_path или сам ответ-массив) до maxRows.
func limitRows(data any, rowsPath string, maxRows int) (rows, total int, truncated bool) {
	if rowsPath == "" {
		arr, ok := data.([]any)
		if !ok {
			return 0, 0, false
		}
		return len(lo.Slice(arr, 0, maxRows)), len(arr), len(arr) > maxRows
	}

	parent, ok := data.(map[string]any)
	keys := strings.Split(rowsPath, ".")
	for _, key := range keys[:len(keys)-1] {
		if !ok {
			return 0, 0, false
		}
		parent, ok = parent[key].(map[string]any)
	}
	if !ok {
		return 0, 0, false
	}
	last := keys[len(keys)-1]
	arr, ok := parent[last].([]any)
	if !ok {
		return 0, 0, false
	}
	if len(arr) > maxRows {
		parent[last] = arr[:maxRows]
	}
	return min(len(arr), maxRows), len(arr), len(arr) > maxRows
}
