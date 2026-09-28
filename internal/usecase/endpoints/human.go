package endpoints

import (
	"github.com/rendau/pulse/internal/util/redact"
)

// humanSanitizer — ответ ручки для человека (audience: human): отдаётся как есть, без проекции на
// схему, но то, чего стандарт не отдаёт никогда, вырезается и здесь: значения полей с именем
// секрета (password, token, dsn…) — маской, в строках — карты маской и учётные данные в адресах.
type humanSanitizer struct {
	pii    PiiI
	masked int
}

func (h *humanSanitizer) value(v any) any {
	switch v := v.(type) {
	case map[string]any:
		result := make(map[string]any, len(v))
		for key, value := range v {
			if redact.SecretName(key) && value != nil {
				h.masked++
				result[h.pii.Text(key)] = redact.Secret()
				continue
			}
			result[h.pii.Text(key)] = h.value(value)
		}
		return result
	case []any:
		result := make([]any, len(v))
		for i, value := range v {
			result[i] = h.value(value)
		}
		return result
	case string:
		return h.pii.Text(v)
	default:
		return v
	}
}
