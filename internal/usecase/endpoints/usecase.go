// Package endpoints — вызов диагностических ручек сервиса из его манифеста
// (docs/service-manifest.md). Самая рискованная часть: allowlist по id, только объявленные
// параметры (строки — по pattern, enum или виду персональных данных), только GET прямо в под,
// к агенту доходят только поля из схемы ответа, персональные — с отметкой вида. Ручка для
// человека (audience: human) — только клиенту, которому это разрешено (req.Human), ответ как
// есть: без проекции, но без секретов и карт.
package endpoints

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/rendau/pulse/internal/constant"
	svcModel "github.com/rendau/pulse/internal/domain/svc/model"
	workloadModel "github.com/rendau/pulse/internal/domain/workload/model"
	"github.com/rendau/pulse/internal/errs"
	k8sModel "github.com/rendau/pulse/internal/service/k8s/model"
	svcproxyModel "github.com/rendau/pulse/internal/service/svcproxy/model"
	"github.com/rendau/pulse/internal/usecase/endpoints/model"
)

// maxErrorChars — текст ошибки ручки в ответе агенту.
const maxErrorChars = 500

// Config — жёсткие потолки (из yaml-правил) поверх декларации ручки.
type Config struct {
	MaxRows      int
	MaxBodyBytes int64
	MaxTimeout   time.Duration
}

type Usecase struct {
	conf Config

	svc      svcServiceI
	workload workloadServiceI
	k8s      k8sClientI
	caller   CallerI
	pii      PiiI
}

func New(conf Config, svc svcServiceI, workload workloadServiceI, k8s k8sClientI, caller CallerI, pii PiiI) *Usecase {
	if conf.MaxRows <= 0 {
		conf.MaxRows = 100
	}
	if conf.MaxBodyBytes <= 0 {
		conf.MaxBodyBytes = 256 << 10
	}
	if conf.MaxTimeout <= 0 {
		conf.MaxTimeout = 10 * time.Second
	}
	return &Usecase{conf: conf, svc: svc, workload: workload, k8s: k8s, caller: caller, pii: pii}
}

var pathParamRe = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)

func (u *Usecase) Call(ctx context.Context, req *model.CallReq) (*model.CallResult, error) {
	service, err := u.svc.GetOrSuggest(ctx, req.Service)
	if err != nil {
		return nil, fmt.Errorf("svc.GetOrSuggest: %w", err)
	}

	// 1. allowlist по id: произвольный путь передать нельзя; ручки — только из манифеста
	endpoint, ok := lo.Find(service.Metadata.Endpoints, func(e svcModel.Endpoint) bool { return e.Id == req.EndpointId })
	if !ok {
		ids := lo.Map(service.Metadata.Endpoints, func(e svcModel.Endpoint, _ int) string { return e.Id })
		if len(ids) == 0 {
			return nil, errs.ErrFull{Err: errs.ObjectNotFound, Desc: fmt.Sprintf("service %s declares no diagnostic endpoints (they come from its manifest, see get_service_info)", service.Name)}
		}
		return nil, errs.ErrFull{Err: errs.ObjectNotFound, Desc: fmt.Sprintf("unknown endpoint_id %q for %s; declared: %s (see get_service_info)",
			req.EndpointId, service.Name, strings.Join(ids, ", "))}
	}
	human := endpoint.Audience == svcModel.AudienceHuman
	if endpoint.Workload == nil || (endpoint.Response == nil && !human) {
		return nil, fmt.Errorf("%w: endpoint %s is not declared in the service manifest", errs.InvalidConfig, endpoint.Id)
	}
	if human && !req.Human {
		return nil, errs.ErrFull{Err: errs.NoPermission, Desc: fmt.Sprintf("endpoint %s of %s is for humans only (audience: human): "+
			"its answer is never given to AI clients — a person gets it through the pulse agent (bot)", endpoint.Id, service.Name)}
	}
	if !strings.HasPrefix(endpoint.Path, "/") || strings.Contains(endpoint.Path, "..") {
		return nil, fmt.Errorf("%w: endpoint %s has invalid path %q", errs.InvalidConfig, endpoint.Id, endpoint.Path)
	}

	// 2. только объявленные параметры: тип, границы, pattern/enum; персональные — к одному виду
	values, err := u.validateParams(endpoint, req.Params)
	if err != nil {
		return nil, err
	}
	path, query := bindParams(endpoint.Path, values)

	// 3. готовый под workload'а, объявившего ручку, на порту манифеста
	target, err := u.target(ctx, service, endpoint)
	if err != nil {
		return nil, err
	}

	timeout := endpoint.Timeout
	if timeout <= 0 || timeout > u.conf.MaxTimeout {
		timeout = u.conf.MaxTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	requestId := newRequestId()
	started := time.Now()
	resp, err := u.caller.GetPod(ctx, target, path, query, map[string]string{
		"User-Agent":         constant.ServiceName + "/" + constant.Version,
		"X-Pulse-Request-Id": requestId,
	}, u.conf.MaxBodyBytes)
	if err != nil {
		// недоступность цели — внятная ошибка, а не таймаут всего вызова
		return nil, fmt.Errorf("%w: endpoint %s of %s (pod %s/%s:%d) is unreachable: %s", errs.ServiceNA,
			endpoint.Id, service.Name, target.Namespace, target.Pod, target.Port, compactError(err))
	}

	result := &model.CallResult{
		Service:    service.Name,
		EndpointId: endpoint.Id,
		Title:      endpoint.Title,
		Audience:   endpoint.Audience,
		RequestId:  requestId,
		StatusCode: resp.StatusCode,
		Duration:   time.Since(started),
		Truncated:  resp.Truncated,
	}

	// 4. ошибка ручки — только её текст, тело ответа не пропускается
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		result.Data = map[string]any{"error": u.errorText(resp.Body)}
		return result, nil
	}
	if resp.Truncated {
		return nil, fmt.Errorf("%w: endpoint %s answered more than %d KB — narrow the parameters", errs.ServiceNA, endpoint.Id, u.conf.MaxBodyBytes>>10)
	}

	var data any
	if err = json.Unmarshal(resp.Body, &data); err != nil {
		return nil, fmt.Errorf("%w: endpoint %s answered not JSON — the service violates the manifest standard", errs.ServiceNA, endpoint.Id)
	}

	// 5. проекция на схему: только объявленные поля, персональные — с отметкой вида, строки — по
	// длине; ручка для человека — как есть, без секретов и карт
	if human {
		h := &humanSanitizer{pii: u.pii}
		result.Data = h.value(data)
		result.MaskedFields = h.masked
	} else {
		p := &projector{pii: u.pii, personal: map[string]string{}}
		result.Data = p.value(data, endpoint.Response, "")
		result.DroppedFields = p.dropped
		result.PersonalFields = p.personal
	}

	// 6. лимит строк: список по rows_path или сам ответ-массив
	maxRows := lo.Clamp(lo.CoalesceOrEmpty(endpoint.MaxRows, u.conf.MaxRows), 1, u.conf.MaxRows)
	result.Rows, result.TotalRows, result.Truncated = limitRows(result.Data, endpoint.RowsPath, maxRows)

	return result, nil
}

// target — готовый под workload'а, объявившего ручку (детерминированно — первый по имени).
func (u *Usecase) target(ctx context.Context, service *svcModel.Main, endpoint svcModel.Endpoint) (svcproxyModel.PodTarget, error) {
	ref := endpoint.Workload
	workloads, _, err := u.workload.List(ctx, &workloadModel.ListReq{ServiceName: new(service.Name)})
	if err != nil {
		return svcproxyModel.PodTarget{}, fmt.Errorf("workload.List: %w", err)
	}
	w, ok := lo.Find(workloads, func(w *workloadModel.Main) bool {
		return w.Namespace == ref.Namespace && w.Kind == ref.Kind && w.Name == ref.Name
	})
	if !ok || w.Manifest.Port == 0 || w.Selector == "" {
		return svcproxyModel.PodTarget{}, fmt.Errorf("%w: workload %s/%s of %s is gone or has no manifest port, nowhere to call", errs.ServiceNA, ref.Namespace, ref.Name, service.Name)
	}

	pods, err := u.k8s.ListPods(ctx, w.Namespace, w.Selector)
	if err != nil {
		return svcproxyModel.PodTarget{}, fmt.Errorf("k8s.ListPods: %w", err)
	}
	ready := lo.Filter(pods, func(p k8sModel.Pod, _ int) bool { return p.Ready && p.IP != "" })
	if len(ready) == 0 {
		return svcproxyModel.PodTarget{}, fmt.Errorf("%w: %s/%s has no ready pods", errs.ServiceNA, w.Namespace, w.Name)
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i].Name < ready[j].Name })

	return svcproxyModel.PodTarget{Namespace: ready[0].Namespace, Pod: ready[0].Name, IP: ready[0].IP, Port: w.Manifest.Port}, nil
}

// errorText — {"error": "…"} из тела ответа с ошибкой; не тот формат — только статус.
func (u *Usecase) errorText(body []byte) string {
	var rep struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &rep) != nil || strings.TrimSpace(rep.Error) == "" {
		return "ручка ответила ошибкой без текста по стандарту"
	}
	return lo.Ellipsis(u.pii.Text(strings.TrimSpace(rep.Error)), maxErrorChars)
}

func newRequestId() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "pulse-" + hex.EncodeToString(b)
}

var urlRe = regexp.MustCompile(`https?://[^\s"]+`)

func compactError(err error) string {
	return urlRe.ReplaceAllString(err.Error(), "<url>")
}
