// Package endpoints — прокси к диагностическим ручкам сервисов (фаза 6). Самая рискованная
// часть: allowlist по id, только объявленные параметры, только GET, валидация до отправки,
// маскирование PII и лимиты из декларации с жёсткими потолками из правил.
package endpoints

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/samber/lo"

	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	"github.com/mechta-market/pulse/internal/errs"
	"github.com/mechta-market/pulse/internal/usecase/endpoints/model"
	"github.com/mechta-market/pulse/internal/util/redact"
)

// Config — жёсткие потолки (из yaml-правил).
type Config struct {
	MaxRows      int
	MaxBodyBytes int64
	MaxTimeout   time.Duration
	DefaultPort  int
}

type Usecase struct {
	conf Config

	svc      svcServiceI
	workload workloadServiceI
	caller   CallerI
}

func New(conf Config, svc svcServiceI, workload workloadServiceI, caller CallerI) *Usecase {
	if conf.MaxRows <= 0 {
		conf.MaxRows = 100
	}
	if conf.MaxBodyBytes <= 0 {
		conf.MaxBodyBytes = 256 << 10
	}
	if conf.MaxTimeout <= 0 {
		conf.MaxTimeout = 10 * time.Second
	}
	if conf.DefaultPort <= 0 {
		conf.DefaultPort = 80
	}
	return &Usecase{conf: conf, svc: svc, workload: workload, caller: caller}
}

var pathParamRe = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)

func (u *Usecase) Call(ctx context.Context, req *model.CallReq) (*model.CallResult, error) {
	service, err := u.svc.GetOrSuggest(ctx, req.Service)
	if err != nil {
		return nil, fmt.Errorf("svc.GetOrSuggest: %w", err)
	}

	// 1. allowlist по id: произвольный путь передать нельзя
	endpoint, ok := lo.Find(service.Metadata.Endpoints, func(e svcModel.Endpoint) bool { return e.Id == req.EndpointId })
	if !ok {
		ids := lo.Map(service.Metadata.Endpoints, func(e svcModel.Endpoint, _ int) string { return e.Id })
		if len(ids) == 0 {
			return nil, errs.ErrFull{Err: errs.ObjectNotFound, Desc: fmt.Sprintf("service %s declares no diagnostic endpoints in service.yaml", service.Name)}
		}
		return nil, errs.ErrFull{Err: errs.ObjectNotFound, Desc: fmt.Sprintf("unknown endpoint_id %q for %s; declared: %s (see get_service_info)",
			req.EndpointId, service.Name, strings.Join(ids, ", "))}
	}
	if method := strings.ToUpper(lo.CoalesceOrEmpty(endpoint.Method, http.MethodGet)); method != http.MethodGet {
		return nil, fmt.Errorf("%w: endpoint %s declares method %s; only GET is allowed", errs.NoPermission, endpoint.Id, method)
	}
	if !strings.HasPrefix(endpoint.Path, "/") || strings.Contains(endpoint.Path, "..") {
		return nil, fmt.Errorf("%w: endpoint %s has invalid path %q", errs.InvalidConfig, endpoint.Id, endpoint.Path)
	}

	// 2. только объявленные параметры, валидация типов и границ
	values, err := validateParams(endpoint, req.Params)
	if err != nil {
		return nil, err
	}
	path, query := bindParams(endpoint.Path, values)

	// 3. адрес внутри кластера: k8s Service и namespace из топологии
	namespace, k8sService, err := u.target(ctx, service, endpoint)
	if err != nil {
		return nil, err
	}
	port := lo.CoalesceOrEmpty(endpoint.Port, u.conf.DefaultPort)

	timeout := endpoint.Timeout
	if timeout <= 0 || timeout > u.conf.MaxTimeout {
		timeout = u.conf.MaxTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	started := time.Now()
	resp, err := u.caller.Get(ctx, namespace, k8sService, port, path, query, u.conf.MaxBodyBytes)
	if err != nil {
		// недоступность цели — внятная ошибка, а не таймаут всего вызова
		return nil, fmt.Errorf("%w: endpoint %s of %s (%s.%s:%d%s) is unreachable: %s", errs.ServiceNA, endpoint.Id, service.Name, k8sService, namespace, port, path, compactError(err))
	}

	result := &model.CallResult{
		Service:    service.Name,
		EndpointId: endpoint.Id,
		Title:      endpoint.Title,
		Url:        fmt.Sprintf("http://%s.%s:%d%s", k8sService, namespace, port, path),
		StatusCode: resp.StatusCode,
		Duration:   time.Since(started),
		Truncated:  resp.Truncated,
	}

	// 4. лимит строк и маскирование PII — до попадания в ответ
	maxRows := endpoint.MaxRows
	if maxRows <= 0 || maxRows > u.conf.MaxRows {
		maxRows = u.conf.MaxRows
	}
	result.Data, result.Rows, result.TotalRows, result.Truncated = decodeBody(resp.Body, resp.Truncated, maxRows)
	if len(endpoint.PII) > 0 {
		result.Data = redact.Fields(result.Data, endpoint.PII)
		result.MaskedKeys = endpoint.PII
	}

	return result, nil
}

// target — namespace и имя k8s Service: из декларации либо по workload'ам сервиса.
func (u *Usecase) target(ctx context.Context, service *svcModel.Main, endpoint svcModel.Endpoint) (string, string, error) {
	workloads, _, err := u.workload.List(ctx, &workloadModel.ListReq{ServiceName: new(service.Name)})
	if err != nil {
		return "", "", fmt.Errorf("workload.List: %w", err)
	}
	if len(workloads) == 0 {
		return "", "", fmt.Errorf("%w: service %s has no workloads in cluster, nowhere to call", errs.ServiceNA, service.Name)
	}

	k8sService := lo.CoalesceOrEmpty(endpoint.K8sService, workloads[0].Name)
	namespace := workloads[0].Namespace
	// если k8s Service совпадает с именем одного из workload'ов — берём его namespace
	if w, ok := lo.Find(workloads, func(w *workloadModel.Main) bool { return w.Name == k8sService }); ok {
		namespace = w.Namespace
	}

	return namespace, k8sService, nil
}

// validateParams: неизвестный параметр — ошибка; тип и границы — по декларации;
// обязательные без дефолта — ошибка.
func validateParams(endpoint svcModel.Endpoint, params map[string]any) (map[string]string, error) {
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
		value := fmt.Sprint(raw)
		if len(value) > 256 || strings.ContainsAny(value, "\r\n") {
			return "", fmt.Errorf("%w: parameter %q is too long or contains line breaks", errs.InvalidRequest, name)
		}
		if def.Max != nil && float64(len(value)) > *def.Max {
			return "", fmt.Errorf("%w: parameter %q must be at most %g characters", errs.InvalidRequest, name, *def.Max)
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

// decodeBody разбирает JSON и режет строки: массив верхнего уровня или первый массив
// внутри объекта считаются «строками».
func decodeBody(body []byte, bodyTruncated bool, maxRows int) (any, int, int, bool) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, 0, 0, bodyTruncated
	}

	var data any
	if err := json.Unmarshal([]byte(trimmed), &data); err != nil {
		return trimmed, 0, 0, bodyTruncated
	}

	switch v := data.(type) {
	case []any:
		total := len(v)
		if total > maxRows {
			return v[:maxRows], maxRows, total, true
		}
		return v, total, total, bodyTruncated
	case map[string]any:
		keys := lo.Keys(v)
		sort.Strings(keys)
		for _, key := range keys {
			rows, ok := v[key].([]any)
			if !ok {
				continue
			}
			total := len(rows)
			if total > maxRows {
				v[key] = rows[:maxRows]
				return v, maxRows, total, true
			}
			return v, total, total, bodyTruncated
		}
	}

	return data, 0, 0, bodyTruncated
}

var urlRe = regexp.MustCompile(`https?://[^\s"]+`)

func compactError(err error) string {
	return urlRe.ReplaceAllString(err.Error(), "<url>")
}
