// Package logs — query_logs: селектор из service.yaml или из топологии, выборка из Loki
// и агрегация в паттерны. Без сервиса — только поиск по тексту во всех логах кластера
// (cluster.go): номер заказа, id клиента; просто «все логи кластера» не отдаются.
package logs

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/samber/lo"

	commonModel "github.com/mechta-market/pulse/internal/domain/common/model"
	logsModel "github.com/mechta-market/pulse/internal/domain/logs/model"
	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	"github.com/mechta-market/pulse/internal/errs"
	"github.com/mechta-market/pulse/internal/usecase/logs/model"
	"github.com/mechta-market/pulse/internal/util/window"
)

// Config — лимиты (из yaml-правил).
type Config struct {
	MaxLines        int
	MaxPatterns     int
	RawLimit        int
	MaxWindow       time.Duration
	DefaultSelector string
	// ClusterSelector — LogQL-селектор всех логов кластера (поиск по всем сервисам, ошибки кластера)
	ClusterSelector string
	// Retention — сколько Loki хранит логи: раньше искать нечего
	Retention time.Duration
	// SearchBudget — время на поиск по всем сервисам назад по дням
	SearchBudget time.Duration
}

type Usecase struct {
	conf Config

	svc      svcServiceI
	workload workloadServiceI
	k8s      k8sClientI
	loki     LokiI
	patterns patternsServiceI
	pii      PiiI
}

func New(conf Config, svc svcServiceI, workload workloadServiceI, k8s k8sClientI, loki LokiI, patterns patternsServiceI, pii PiiI) *Usecase {
	if conf.MaxWindow <= 0 {
		conf.MaxWindow = 24 * time.Hour
	}
	if conf.Retention <= 0 {
		conf.Retention = 30 * 24 * time.Hour
	}
	if conf.SearchBudget <= 0 {
		conf.SearchBudget = time.Minute
	}
	if conf.ClusterSelector == "" {
		conf.ClusterSelector = `{kubernetes_namespace_name=~".+"}`
	}
	return &Usecase{conf: conf, svc: svc, workload: workload, k8s: k8s, loki: loki, patterns: patterns, pii: pii}
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

	if req.Mode != "" && req.Mode != model.ModePatterns && req.Mode != model.ModeRaw {
		return nil, fmt.Errorf("%w: mode %q; expected patterns or raw", errs.InvalidRequest, req.Mode)
	}

	// телефон (+…) — в любом написании, email — без регистра: подстрока-предфильтр для Loki и
	// точный регэксп
	var prefilter string
	searched := strings.TrimSpace(req.Pattern)
	if req.Pattern != "" {
		prefilter, req.Pattern = u.pii.SearchPattern(strings.TrimSpace(req.Pattern))
		if _, err := regexp.Compile(req.Pattern); err != nil {
			return nil, fmt.Errorf("%w: pattern is not a valid regexp: %s", errs.InvalidRequest, err)
		}
	}

	if req.Window > u.conf.MaxWindow {
		return nil, fmt.Errorf("%w: window %s exceeds maximum %s for logs; narrow the window", errs.InvalidRequest, req.Window, u.conf.MaxWindow)
	}

	result, err := u.query(ctx, req, level, prefilter)
	if err == nil && strings.TrimSpace(req.Service) == "" {
		result.IdMatches = u.idMatches(ctx, searched)
	}
	return result, err
}

// idMatches — чьим объектом может быть искомый номер: формат номера из раздела domain
// манифестов (id_pattern). Подсказка: ошибка каталога её просто убирает.
func (u *Usecase) idMatches(ctx context.Context, id string) []model.IdMatch {
	if id == "" || regexp.QuoteMeta(id) != id {
		return nil
	}
	services, _, err := u.svc.List(ctx, &svcModel.ListReq{ListParams: commonModel.ListParams{PageSize: 1000}, HasMetadata: new(true)})
	if err != nil {
		slog.Debug("logs: catalog is unavailable for id hints", "error", err)
		return nil
	}
	var result []model.IdMatch
	for _, s := range services {
		if s.Metadata.Domain == nil {
			continue
		}
		for _, e := range s.Metadata.Domain.Entities {
			if e.IdPattern == "" {
				continue
			}
			if re, err := regexp.Compile(`^(?:` + e.IdPattern + `)$`); err == nil && re.MatchString(id) {
				result = append(result, model.IdMatch{Service: s.Name, Entity: e.Name})
			}
		}
	}
	return result
}

func (u *Usecase) query(ctx context.Context, req *model.QueryReq, level, prefilter string) (*model.QueryResult, error) {
	if strings.TrimSpace(req.Service) == "" {
		return u.search(ctx, req, level, prefilter)
	}

	mode := lo.CoalesceOrEmpty(req.Mode, model.ModePatterns)
	limit := u.rawLimit(req.Limit)
	if mode != model.ModeRaw {
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

	start, end, err := u.period(req, window.Default)
	if err != nil {
		return nil, err
	}

	lines, selector, source, err := u.collect(ctx, service, groups, prefilter, req.Pattern, level, start, end, limit)
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

// period — окно запроса: window назад от end (end пусто — сейчас). end днём без window — эти
// сутки целиком. Окно не выходит за «сейчас» и за срок хранения логов.
func (u *Usecase) period(req *model.QueryReq, def time.Duration) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	oldest := now.Add(-u.conf.Retention)

	end := lo.Ternary(req.End.IsZero(), now, req.End.UTC())
	win := lo.Ternary(req.Window > 0, req.Window, lo.Ternary(req.EndIsDay, 24*time.Hour, def))
	if !end.After(oldest) {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: logs are kept for %s; end %s is older", errs.InvalidRequest, window.Format(u.conf.Retention), end.Format(time.RFC3339))
	}

	start := lo.Latest(end.Add(-win), oldest)
	return start, lo.Earliest(end, now), nil
}

// rawLimit — число строк в режиме raw: запрошенное, но не больше raw_limit.
func (u *Usecase) rawLimit(limit int) int {
	if limit <= 0 || limit > u.conf.RawLimit {
		return u.conf.RawLimit
	}
	return limit
}

func (u *Usecase) TopErrors(ctx context.Context, service *svcModel.Main, workloads []*workloadModel.Main, win time.Duration, top int) ([]logsModel.Pattern, error) {
	groups := u.podGroups(ctx, service, workloads)

	end := time.Now().UTC()
	lines, _, _, err := u.collect(ctx, service, groups, "", "", logsModel.LevelError, end.Add(-win), end, u.conf.MaxLines)
	if err != nil {
		return nil, err
	}

	return u.patterns.Aggregate(lines, top), nil
}

// collect — строки из Loki, а если он не подключён или не ответил — из Kubernetes API
// (живые поды сервиса). Возвращает и то, откуда и по какому селектору строки взяты.
func (u *Usecase) collect(ctx context.Context, service *svcModel.Main, groups []podGroup, prefilter, pattern, level string, start, end time.Time, limit int) ([]logsModel.Line, string, string, error) {
	selector, err := u.selector(service, groups)
	if err != nil {
		return nil, "", "", err
	}

	var lokiErr error
	if u.loki != nil {
		lines, err := u.fetch(ctx, logQL(selector, prefilter, pattern, level), level, start, end, limit, func(labels map[string]string, line *logsModel.Line) {
			line.Workload = groupOfPod(podOf(labels), groups)
		})
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

// logQL — селектор с фильтрами. Фильтр по уровню — грубое регулярное выражение (сужает
// выдачу на стороне Loki), точно уровень определяется по строке в fetch. Паттерн без
// метасимволов ищется как подстрока (|=): Loki проверяет её быстрее регэкспа; prefilter —
// подстрока перед регэкспом (поиск телефона: регэксп проверяется только на отобранных строках).
func logQL(selector, prefilter, pattern, level string) string {
	query := selector
	if level != "" {
		query += ` |~ "(?i)` + levelRegexp(level) + `"`
	}
	if prefilter != "" {
		query += " |= " + strconv.Quote(prefilter)
	}
	if pattern != "" {
		op := lo.Ternary(regexp.QuoteMeta(pattern) == pattern, "|=", "|~")
		query += " " + op + " " + strconv.Quote(pattern)
	}
	return query
}

// fetch забирает строки по LogQL, размечает уровень и владельца (attach — по лейблам потока:
// workload сервиса или сервис при поиске по всему кластеру). Строки с другим уровнем
// отбрасываются. Карты и учётные данные вырезаются сразу (pii.Text): дальше — в паттерны,
// примеры и ответ — уходит очищенное; телефоны и email от модели прячет агент.
func (u *Usecase) fetch(ctx context.Context, query, level string, start, end time.Time, limit int, attach func(labels map[string]string, line *logsModel.Line)) ([]logsModel.Line, error) {
	streams, err := u.loki.QueryRange(ctx, query, start, end, limit)
	if err != nil {
		return nil, fmt.Errorf("loki.QueryRange: %w", err)
	}

	lines := make([]logsModel.Line, 0, 256)
	for _, stream := range streams {
		streamLevel := u.patterns.DetectLevel("level=" + stream.Labels["level"])
		owner := logsModel.Line{}
		attach(stream.Labels, &owner)
		for _, e := range stream.Entries {
			line := owner
			line.TS, line.Text, line.Level = e.TS, u.pii.Text(e.Line), u.patterns.DetectLevel(e.Line)
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
