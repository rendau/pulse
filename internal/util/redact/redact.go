// Package redact — маскирование конфигурации и секретов (ТЗ 4.2). Порядок проверок:
// deny-список имени ключа → allowlist значения → маска. Значения секретов не возвращаются
// никогда — для них есть только Secret().
package redact

import (
	"regexp"
	"strings"
)

const Mask = "***"

// denyKeyRe — имя ключа выдаёт секрет независимо от значения.
var denyKeyRe = regexp.MustCompile(`(?i)(PASSWORD|PASSWD|PASS\b|PWD|SECRET|TOKEN|KEY|DSN|CREDENTIAL|PRIVATE|CERT|AUTH|COOKIE|SALT|SIGN)`)

// allowlist значений: то, что заведомо не секрет и полезно модели
var allowValueRes = []*regexp.Regexp{
	regexp.MustCompile(`^(?i)(true|false|yes|no|on|off|enabled|disabled)$`),
	regexp.MustCompile(`^-?\d+(\.\d+)?$`),                    // числовой лимит
	regexp.MustCompile(`^\d+(\.\d+)?(ns|us|µs|ms|s|m|h|d)$`), // duration
	regexp.MustCompile(`^\d+(\.\d+)?(Ki|Mi|Gi|K|M|G|KB|MB|GB)?B?$`),
	// URL без userinfo: scheme://host[:port][/path][?query]
	regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://[^@\s/]+(/[^\s]*)?$`),
	// hostname[:port], в т.ч. svc.cluster.local; список хостов через запятую (брокеры)
	regexp.MustCompile(`^([a-zA-Z0-9][a-zA-Z0-9-]*(\.[a-zA-Z0-9][a-zA-Z0-9-]*)*(:\d{1,5})?)(,\s*[a-zA-Z0-9][a-zA-Z0-9-]*(\.[a-zA-Z0-9][a-zA-Z0-9-]*)*(:\d{1,5})?)*$`),
	regexp.MustCompile(`^(debug|info|warn|warning|error|fatal|trace|json|text|prod|production|stage|staging|dev|development|test)$`),
	regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`), // короткий идентификатор: имя топика, региона, режима
	regexp.MustCompile(`^(/[A-Za-z0-9._~-]+)+/?$|^/$`),    // путь без query: /mcp, /api/v1
}

// KeyDenied — имя ключа матчится на deny-список.
func KeyDenied(key string) bool {
	return denyKeyRe.MatchString(key)
}

// ValueAllowed — значение проходит allowlist (URL, hostname, булев флаг, числовой лимит…).
func ValueAllowed(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	for _, re := range allowValueRes {
		if re.MatchString(value) {
			return true
		}
	}
	return false
}

// Value возвращает значение env/configmap для ответа: deny-ключ → маска, allowlist → как есть,
// иначе маска.
func Value(key, value string) string {
	if KeyDenied(key) {
		return Mask
	}
	if ValueAllowed(value) {
		return strings.TrimSpace(value)
	}
	return Mask
}

// Secret — значение секрета не возвращается никогда.
func Secret() string {
	return Mask
}

// Fields маскирует в объекте поля из списка pii (без учёта регистра), рекурсивно по вложенным
// объектам и массивам. Используется для ответов диагностических ручек (фаза 6).
func Fields(obj any, pii []string) any {
	if len(pii) == 0 {
		return obj
	}
	deny := make(map[string]struct{}, len(pii))
	for _, f := range pii {
		deny[strings.ToLower(f)] = struct{}{}
	}
	return maskFields(obj, deny)
}

func maskFields(obj any, deny map[string]struct{}) any {
	switch v := obj.(type) {
	case map[string]any:
		result := make(map[string]any, len(v))
		for key, value := range v {
			if _, ok := deny[strings.ToLower(key)]; ok {
				result[key] = Mask
				continue
			}
			result[key] = maskFields(value, deny)
		}
		return result
	case []any:
		result := make([]any, len(v))
		for i, item := range v {
			result[i] = maskFields(item, deny)
		}
		return result
	default:
		return obj
	}
}
