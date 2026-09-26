package endpoints

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/samber/lo"

	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	"github.com/mechta-market/pulse/internal/errs"
)

// validateParams: неизвестный параметр — ошибка; тип, границы, pattern и enum — по декларации;
// персональный параметр — значение, приведённое к одному виду (токены раскрывает агент);
// обязательные без дефолта — ошибка.
func (u *Usecase) validateParams(endpoint svcModel.Endpoint, params map[string]any) (map[string]string, error) {
	values := make(map[string]string, len(endpoint.Params))

	for name := range params {
		if _, ok := endpoint.Params[name]; !ok {
			declared := lo.Keys(endpoint.Params)
			sort.Strings(declared)
			return nil, fmt.Errorf("%w: parameter %q is not declared for endpoint %s; declared: %s", errs.InvalidRequest, name, endpoint.Id, strings.Join(declared, ", "))
		}
	}

	names := lo.Keys(endpoint.Params)
	sort.Strings(names)
	for _, name := range names {
		def := endpoint.Params[name]
		raw, present := params[name]
		if !present || raw == nil {
			if def.Default != "" {
				values[name] = def.Default
				continue
			}
			if def.Required || strings.Contains(endpoint.Path, "{"+name+"}") {
				return nil, fmt.Errorf("%w: parameter %q is required for endpoint %s", errs.InvalidRequest, name, endpoint.Id)
			}
			continue
		}

		value, err := coerce(name, def, raw)
		if err != nil {
			return nil, err
		}
		if def.Personal != "" {
			if value, err = u.pii.Normalize(def.Personal, value); err != nil {
				return nil, fmt.Errorf("parameter %q: %w", name, err)
			}
		}
		values[name] = value
	}

	return values, nil
}

func coerce(name string, def svcModel.EndpointParam, raw any) (string, error) {
	switch strings.ToLower(lo.CoalesceOrEmpty(def.Type, "string")) {
	case "int", "integer", "float", "number":
		var num float64
		switch v := raw.(type) {
		case float64:
			num = v
		case int:
			num = float64(v)
		case string:
			parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				return "", fmt.Errorf("%w: parameter %q must be a number", errs.InvalidRequest, name)
			}
			num = parsed
		default:
			return "", fmt.Errorf("%w: parameter %q must be a number", errs.InvalidRequest, name)
		}
		if strings.HasPrefix(strings.ToLower(def.Type), "int") && num != float64(int64(num)) {
			return "", fmt.Errorf("%w: parameter %q must be an integer", errs.InvalidRequest, name)
		}
		if def.Min != nil && num < *def.Min {
			return "", fmt.Errorf("%w: parameter %q must be >= %g", errs.InvalidRequest, name, *def.Min)
		}
		if def.Max != nil && num > *def.Max {
			return "", fmt.Errorf("%w: parameter %q must be <= %g", errs.InvalidRequest, name, *def.Max)
		}
		return strconv.FormatFloat(num, 'f', -1, 64), nil
	case "bool", "boolean":
		switch v := raw.(type) {
		case bool:
			return strconv.FormatBool(v), nil
		case string:
			parsed, err := strconv.ParseBool(strings.TrimSpace(v))
			if err != nil {
				return "", fmt.Errorf("%w: parameter %q must be a boolean", errs.InvalidRequest, name)
			}
			return strconv.FormatBool(parsed), nil
		default:
			return "", fmt.Errorf("%w: parameter %q must be a boolean", errs.InvalidRequest, name)
		}
	default:
		value := strings.TrimSpace(fmt.Sprint(raw))
		if len(value) > 256 || strings.ContainsAny(value, "\r\n") {
			return "", fmt.Errorf("%w: parameter %q is too long or contains line breaks", errs.InvalidRequest, name)
		}
		if len(def.Enum) > 0 && !slices.Contains(def.Enum, value) {
			return "", fmt.Errorf("%w: parameter %q must be one of %s", errs.InvalidRequest, name, strings.Join(def.Enum, ", "))
		}
		if def.Pattern != "" {
			// шаблон — на всё значение: частичное совпадение пропустило бы что угодно вокруг
			re, err := regexp.Compile(`^(?:` + def.Pattern + `)$`)
			if err != nil || !re.MatchString(value) {
				return "", fmt.Errorf("%w: parameter %q must match %s", errs.InvalidRequest, name, def.Pattern)
			}
		}
		return value, nil
	}
}

// bindParams подставляет {name} в путь (с экранированием), остальное — в query.
func bindParams(path string, values map[string]string) (string, map[string]string) {
	query := make(map[string]string, len(values))
	used := make(map[string]struct{}, 2)

	bound := pathParamRe.ReplaceAllStringFunc(path, func(m string) string {
		name := m[1 : len(m)-1]
		if v, ok := values[name]; ok {
			used[name] = struct{}{}
			return url.PathEscape(v)
		}
		return m
	})

	for name, v := range values {
		if _, ok := used[name]; !ok {
			query[name] = v
		}
	}

	return bound, query
}
