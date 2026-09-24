// Package logs — query_logs: селектор из service.yaml или из топологии, выборка из Loki
// и агрегация в паттерны. Запрос без привязки к сервису невозможен (критерий фазы 3).
package logs

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/samber/lo"

	logsModel "github.com/mechta-market/pulse/internal/domain/logs/model"
	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	"github.com/mechta-market/pulse/internal/errs"
	"github.com/mechta-market/pulse/internal/usecase/logs/model"
	"github.com/mechta-market/pulse/internal/util/redact"
	"github.com/mechta-market/pulse/internal/util/window"
)

// Config — лимиты (из yaml-правил).
type Config struct {
	MaxLines        int
	MaxPatterns     int
	RawLimit        int
	MaxWindow       time.Duration
	DefaultSelector string
}

type Usecase struct {
	conf Config

	svc      svcServiceI
	workload workloadServiceI
	k8s      k8sClientI
	loki     LokiI
	patterns patternsServiceI
}

func New(conf Config, svc svcServiceI, workload workloadServiceI, k8s k8sClientI, loki LokiI, patterns patternsServiceI) *Usecase {
	if conf.MaxWindow <= 0 {
		conf.MaxWindow = 24 * time.Hour
	}
	return &Usecase{conf: conf, svc: svc, workload: workload, k8s: k8s, loki: loki, patterns: patterns}
}

var allowedLevels = []string{logsModel.LevelError, logsModel.LevelWarn, logsModel.LevelInfo, logsModel.LevelDebug}

func (u *Usecase) Query(ctx context.Context, req *model.QueryReq) (*model.QueryResult, error) {
	level := strings.ToLower(strings.TrimSpace(req.Level))
	if level == "warning" {
		level = logsModel.LevelWarn
	}
	if level != "" && !lo.Contains(allowedLevels, level) {
		return nil, fmt.Errorf("%w: level %q; expected one of %s", errs.InvalidRequest, req.Level, strings.Join(allowedLevels, ", "))
	}

	mode := lo.CoalesceOrEmpty(req.Mode, model.ModePatterns)
	if mode != model.ModePatterns && mode != model.ModeRaw {
		return nil, fmt.Errorf("%w: mode %q; expected patterns or raw", errs.InvalidRequest, req.Mode)
	}

	if req.Pattern != "" {
		if _, err := regexp.Compile(req.Pattern); err != nil {
			return nil, fmt.Errorf("%w: pattern is not a valid regexp: %s", errs.InvalidRequest, err)
		}
	}

	win := req.Window
	if win <= 0 {
		win = window.Default
	}
	if win > u.conf.MaxWindow {
		return nil, fmt.Errorf("%w: window %s exceeds maximum %s for logs; narrow the window", errs.InvalidRequest, win, u.conf.MaxWindow)
	}

	limit := req.Limit
	if mode == model.ModeRaw {
		if limit <= 0 || limit > u.conf.RawLimit {
			limit = u.conf.RawLimit
		}
	} else {
		limit = u.conf.MaxLines
	}

	service, err := u.svc.GetOrSuggest(ctx, req.Service)
	if err != nil {
		return nil, fmt.Errorf("svc.GetOrSuggest: %w", err)
	}
	workloads, _, err := u.workload.List(ctx, &workloadModel.ListReq{ServiceName: new(service.Name)})
	if err != nil {
		return nil, fmt.Errorf("workload.List: %w", err)
	}
	groups := u.podGroups(ctx, service, workloads)
	names := lo.Map(groups, func(g podGroup, _ int) string { return g.Name })

	// один workload сервиса: сужаем селектор (свой селектор из service.yaml — фильтром строк)
	if w := strings.TrimSpace(req.Workload); w != "" {
		if !lo.Contains(names, w) {
			return nil, fmt.Errorf("%w: workload %q is not part of service %s; expected one of: %s",
				errs.InvalidRequest, w, service.Name, strings.Join(names, ", "))
		}
		groups = lo.Filter(groups, func(g podGroup, _ int) bool { return g.Name == w })
	}

	end := time.Now().UTC()
	start := end.Add(-win)

	lines, selector, source, err := u.collect(ctx, service, groups, req.Pattern, level, start, end, limit)
	if err != nil {
		return nil, err
	}
	if w := strings.TrimSpace(req.Workload); w != "" {
		lines = lo.Filter(lines, func(l logsModel.Line, _ int) bool { return l.Workload == w })
	}

	result := &model.QueryResult{
		Service:    service.Name,
		Selector:   selector,
		Source:     source,
		Mode:       mode,
		Start:      start,
		End:        end,
		TotalLines: len(lines),
		Truncated:  len(lines) >= limit,
	}

	if mode == model.ModeRaw {
		result.Lines = lines
		return result, nil
	}

	result.Patterns = u.patterns.Aggregate(lines, u.conf.MaxPatterns)
	return result, nil
}

func (u *Usecase) TopErrors(ctx context.Context, service *svcModel.Main, workloads []*workloadModel.Main, win time.Duration, top int) ([]logsModel.Pattern, error) {
	groups := u.podGroups(ctx, service, workloads)

	end := time.Now().UTC()
	lines, _, _, err := u.collect(ctx, service, groups, "", logsModel.LevelError, end.Add(-win), end, u.conf.MaxLines)
	if err != nil {
		return nil, err
	}

	return u.patterns.Aggregate(lines, top), nil
}

// collect — строки из Loki, а если он не подключён или не ответил — из Kubernetes API
// (живые поды сервиса). Возвращает и то, откуда и по какому селектору строки взяты.
func (u *Usecase) collect(ctx context.Context, service *svcModel.Main, groups []podGroup, pattern, level string, start, end time.Time, limit int) ([]logsModel.Line, string, string, error) {
	selector, err := u.selector(service, groups)
	if err != nil {
		return nil, "", "", err
	}

	var lokiErr error
	if u.loki != nil {
		lines, err := u.fetch(ctx, selector, pattern, level, start, end, limit, groups)
		if err == nil {
			return lines, selector, model.SourceLoki, nil
		}
		lokiErr = err
	}
	if u.k8s == nil || len(groups) == 0 {
		return nil, "", "", lo.Ternary(lokiErr != nil, lokiErr, fmt.Errorf("%w: no log source: loki is not configured (LOKI_URL)", errs.ServiceNA))
	}

	lines, err := u.fetchK8s(ctx, groups, pattern, level, start, end, limit)
	if err != nil {
		if lokiErr != nil {
			return nil, "", "", fmt.Errorf("%w; kubernetes fallback: %w", lokiErr, err)
		}
		return nil, "", "", err
	}
	return lines, "pods " + groups[0].Namespace + "/" + podRegex(groups), model.SourceKubernetes, nil
}

// selector — LogQL-селектор сервиса: из service.yaml, иначе по шаблону из топологии
// (workload'ы и Job'ы оркестратора — podGroups).
func (u *Usecase) selector(service *svcModel.Main, groups []podGroup) (string, error) {
	if s := strings.TrimSpace(service.Metadata.Logs.Selector); s != "" {
		return s, nil
	}
	if len(groups) == 0 {
		return "", fmt.Errorf("%w: service %s has no workloads in cluster and no logs.selector in service.yaml", errs.InvalidRequest, service.Name)
	}

	return strings.NewReplacer(
		"{namespace}", groups[0].Namespace,
		"{pod_regex}", podRegex(groups),
		"{service}", service.Name,
		"{workloads}", strings.Join(lo.FilterMap(groups, func(g podGroup, _ int) (string, bool) { return g.Name, !g.Jobs }), "|"),
	).Replace(u.conf.DefaultSelector), nil
}

// fetch собирает LogQL (селектор + фильтры), забирает строки и размечает уровень и workload.
// Фильтр по уровню применяется в LogQL как грубое регулярное выражение (сужает выдачу
// на стороне Loki) и затем точно — по определённому уровню строки. PII в тексте строк
// маскируется сразу (redact.Text): дальше — в паттерны, примеры и ответ — уходит маскированное.
// groups — workload'ы и Job'ы сервиса: по ним строка привязывается к workload'у через под.
func (u *Usecase) fetch(ctx context.Context, selector, pattern, level string, start, end time.Time, limit int, groups []podGroup) ([]logsModel.Line, error) {
	query := selector
	if level != "" {
		query += ` |~ "(?i)` + levelRegexp(level) + `"`
	}
	if pattern != "" {
		query += ` |~ ` + fmt.Sprintf("%q", pattern)
	}

	streams, err := u.loki.QueryRange(ctx, query, start, end, limit)
	if err != nil {
		return nil, fmt.Errorf("loki.QueryRange: %w", err)
	}

	lines := make([]logsModel.Line, 0, 256)
	for _, stream := range streams {
		streamLevel := u.patterns.DetectLevel("level=" + stream.Labels["level"])
		workload := groupOfPod(podOf(stream.Labels), groups)
		for _, e := range stream.Entries {
			line := logsModel.Line{TS: e.TS, Text: redact.Text(e.Line), Level: u.patterns.DetectLevel(e.Line), Workload: workload}
			if line.Level == "" {
				line.Level = streamLevel
			}
			if level != "" && line.Level != level {
				continue
			}
			lines = append(lines, line)
		}
	}

	sort.SliceStable(lines, func(i, j int) bool { return lines[i].TS.After(lines[j].TS) })
	if len(lines) > limit {
		lines = lines[:limit]
	}

	return lines, nil
}

// podLabels — лейбл пода в разных сборщиках логов: promtail/alloy, fluent-bit, OTel.
var podLabels = []string{"pod", "kubernetes_pod_name", "k8s_pod_name", "pod_name"}

func podOf(labels map[string]string) string {
	for _, key := range podLabels {
		if pod := labels[key]; pod != "" {
			return pod
		}
	}
	return ""
}

func workloadNames(workloads []*workloadModel.Main) []string {
	names := lo.Uniq(lo.Map(workloads, func(w *workloadModel.Main, _ int) string { return w.Name }))
	sort.Strings(names)
	return names
}

func levelRegexp(level string) string {
	switch level {
	case logsModel.LevelError:
		return `error|fatal|panic|critical`
	case logsModel.LevelWarn:
		return `warn`
	case logsModel.LevelInfo:
		return `info`
	default:
		return `debug|trace`
	}
}
