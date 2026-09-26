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
	"github.com/samber/lo/mutable"
	"golang.org/x/sync/errgroup"

	logsModel "github.com/mechta-market/pulse/internal/domain/logs/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	"github.com/mechta-market/pulse/internal/errs"
	"github.com/mechta-market/pulse/internal/usecase/logs/model"
	"github.com/mechta-market/pulse/internal/util/podname"
)

// minSearchLen — короче искать по всем логам кластера бессмысленно: совпадёт почти всё.
const minSearchLen = 3

// search — поиск текста (номер заказа, id клиента) во всех логах кластера: «что по заказу
// 234115» — все следы по всем сервисам. Строки привязываются к сервисам каталога по поду,
// в ответе — где и сколько нашлось. Без окна и конца — назад по дням (scan); режим по
// умолчанию raw (в паттернах номер замаскирован), строки — по времени.
// Только Loki: обойти логи всех подов через Kubernetes API за разумное время нельзя.
func (u *Usecase) search(ctx context.Context, req *model.QueryReq, level, prefilter string) (*model.QueryResult, error) {
	pattern := strings.TrimSpace(req.Pattern)
	if len([]rune(pattern)) < minSearchLen {
		return nil, fmt.Errorf("%w: service is required; to search all services pass pattern of %d+ characters (order number, customer id…)", errs.InvalidRequest, minSearchLen)
	}
	if strings.TrimSpace(req.Workload) != "" {
		return nil, fmt.Errorf("%w: workload requires service", errs.InvalidRequest)
	}
	if u.loki == nil {
		return nil, fmt.Errorf("%w: search across all services requires Loki (LOKI_URL is not configured); pass service", errs.ServiceNA)
	}

	mode := lo.CoalesceOrEmpty(req.Mode, model.ModeRaw)
	owners, err := u.owners(ctx)
	if err != nil {
		return nil, err
	}
	query := searchQL(u.conf.ClusterSelector, prefilter, pattern, level)

	// выборка — max_lines: счётчики по сервисам точнее, чем по показанным строкам
	var lines []logsModel.Line
	var start, end time.Time
	var stop string
	if req.End.IsZero() && req.Window <= 0 {
		lines, start, end, stop, err = u.scan(ctx, query, level, owners.attach)
	} else {
		if start, end, err = u.period(req, u.conf.MaxWindow); err == nil {
			lines, err = u.fetch(ctx, query, level, start, end, u.conf.MaxLines, owners.attach)
		}
	}
	if err != nil {
		return nil, err
	}

	result := &model.QueryResult{
		Selector:   u.conf.ClusterSelector,
		Source:     model.SourceLoki,
		Mode:       mode,
		Start:      start,
		End:        end,
		TotalLines: len(lines),
		Truncated:  len(lines) >= u.conf.MaxLines,
		Services:   serviceHits(lines),
		SearchStop: stop,
	}

	if mode == model.ModeRaw {
		// последние строки, но по порядку событий — след заказа читается сверху вниз
		shown := lines[:min(len(lines), u.rawLimit(req.Limit))]
		result.Truncated = result.Truncated || len(shown) < len(lines)
		result.Lines = append([]logsModel.Line{}, shown...)
		mutable.Reverse(result.Lines)
		return result, nil
	}

	// workload'ы разных сервисов в одном паттерне ничего не добавляют к списку сервисов
	result.Patterns = lo.Map(u.patterns.Aggregate(lines, u.conf.MaxPatterns), func(p logsModel.Pattern, _ int) logsModel.Pattern {
		p.Workloads = nil
		return p
	})
	return result, nil
}

// emptyDaysAfterHits — сколько пустых суток подряд перед найденными следами завершают поиск:
// между оформлением и доставкой заказа бывают дни тишины.
const emptyDaysAfterHits = 2

// scan — поиск назад по суткам: у Loki нет индекса по словам, и любой поиск читает все логи
// окна, поэтому — сутки за запросом (≈2 с), по одному, с остановкой, как только следы
// найдены и перед ними пусто. Предел — срок хранения логов и бюджет времени; не уложились —
// отдаём найденное, в start — докуда успели.
func (u *Usecase) scan(ctx context.Context, query, level string, attach func(map[string]string, *logsModel.Line)) ([]logsModel.Line, time.Time, time.Time, string, error) {
	budgetCtx, cancel := context.WithTimeout(ctx, u.conf.SearchBudget)
	defer cancel()

	now := time.Now().UTC()
	oldest := now.Add(-u.conf.Retention)

	var lines []logsModel.Line
	covered, empty := now, 0
	for {
		start := lo.Latest(covered.Add(-24*time.Hour), oldest)
		day, err := u.fetch(budgetCtx, query, level, start, covered, u.conf.MaxLines-len(lines), attach)
		if err != nil {
			// бюджет кончился посреди суток: отдаём проверенное, если оно есть
			if ctx.Err() == nil && budgetCtx.Err() != nil && covered.Before(now) {
				return lines, covered, now, model.SearchStopBudget, nil
			}
			return nil, time.Time{}, time.Time{}, "", err
		}
		lines = append(lines, day...)
		covered = start

		switch {
		case len(day) > 0:
			empty = 0
		case len(lines) > 0:
			empty++
		}
		switch {
		case len(lines) >= u.conf.MaxLines:
			return lines, covered, now, model.SearchStopLimit, nil
		case empty >= emptyDaysAfterHits:
			return lines, covered, now, model.SearchStopFound, nil
		case !covered.After(oldest):
			return lines, covered, now, model.SearchStopRetention, nil
		}
	}
}

// searchQL — LogQL поиска. Идентификатор (без метасимволов, по краям буква или цифра) ищется
// отдельно стоящим: подстрокой в Loki (быстро) и затем регэкспом с границами — рядом не
// буква и не цифра (номер 234115 не находится внутри 12341156, а «halyk__41314079» и
// «ord-41314079» находятся: _ и - — разделители, \b их не пропускал), у числа ещё и не
// точка перед ним (микросекунды «28.234115» во временных метках klog). \b234115\b от модели — тот же идентификатор: регэксп без
// подстроки Loki проверяет на порядок медленнее.
func searchQL(selector, prefilter, pattern, level string) string {
	if inner, ok := strings.CutPrefix(pattern, `\b`); ok {
		if inner, ok = strings.CutSuffix(inner, `\b`); ok && regexp.QuoteMeta(inner) == inner {
			pattern = inner
		}
	}

	query := logQL(selector, prefilter, pattern, level)
	if prefilter == "" && regexp.QuoteMeta(pattern) == pattern && wordEdgesRe.MatchString(pattern) {
		left := `(?:^|[^[:alnum:]])`
		if digitsRe.MatchString(pattern) {
			left = `(?:^|[^[:alnum:].])`
		}
		query += " |~ " + strconv.Quote(left+pattern+`(?:[^[:alnum:]]|$)`)
	}
	return query
}

var (
	wordEdgesRe = regexp.MustCompile(`^[[:alnum:]](?:.*[[:alnum:]])?$`)
	digitsRe    = regexp.MustCompile(`^\d+$`)
)

// serviceHits — сколько строк нашлось у каждого сервиса (под не из каталога — по namespace'у),
// больше всего — первым.
func serviceHits(lines []logsModel.Line) []logsModel.ServiceHits {
	byOwner := make(map[string]*logsModel.ServiceHits, 8)
	for _, line := range lines {
		key := line.Service + "\x00" + line.Namespace
		h, ok := byOwner[key]
		if !ok {
			h = &logsModel.ServiceHits{Service: line.Service, Namespace: line.Namespace, FirstSeen: line.TS, LastSeen: line.TS}
			byOwner[key] = h
		}
		h.Count++
		h.FirstSeen = lo.Earliest(h.FirstSeen, line.TS)
		h.LastSeen = lo.Latest(h.LastSeen, line.TS)
	}

	hits := lo.MapToSlice(byOwner, func(_ string, h *logsModel.ServiceHits) logsModel.ServiceHits { return *h })
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Count != hits[j].Count {
			return hits[i].Count > hits[j].Count
		}
		return hits[i].Service+hits[i].Namespace < hits[j].Service+hits[j].Namespace
	})
	return hits
}

// errorLineRe — error-строка в логах всего кластера. Строже, чем фильтр логов одного сервиса:
// счётчик идёт прямо из Loki, без уточнения уровня по строке, поэтому слово error в
// info-строке («errors=0») не должно считаться. Уровень — полем level/severity (в том числе
// внутри обёртки сборщика, где кавычки экранированы; detected_level в логах запросов Loki —
// не уровень) или словом ERROR/FATAL/PANIC капсом.
const errorLineRe = `(?i:\b(?:level|lvl|severity)\\?"?\s*[:=]\s*\\?"?(?:error|err|fatal|panic|crit|critical)\b)|\b(?:ERROR|FATAL|PANIC|CRITICAL)\b|\bpanic: `

// namespaceLabels — лейбл namespace'а в разных сборщиках логов: promtail/alloy, fluent-bit, OTel.
var namespaceLabels = []string{"namespace", "kubernetes_namespace_name", "k8s_namespace_name"}

func namespaceOf(labels map[string]string) string {
	for _, key := range namespaceLabels {
		if ns := labels[key]; ns != "" {
			return ns
		}
	}
	return ""
}

// ClusterErrors — выжимка ошибок по всем логам кластера: точный счётчик error-строк по подам
// (count_over_time в Loki) → сервисы каталога, у top сервисов — самый частый паттерн из
// выборки последних строк. Окно ограничено потолком окна логов.
func (u *Usecase) ClusterErrors(ctx context.Context, win time.Duration, top int) (*logsModel.ClusterErrors, error) {
	if u.loki == nil {
		return nil, fmt.Errorf("%w: loki is not configured (LOKI_URL)", errs.ServiceNA)
	}
	win = min(win, u.conf.MaxWindow)

	owners, err := u.owners(ctx)
	if err != nil {
		return nil, err
	}

	end := time.Now().UTC()
	// сначала дешёвый фильтр подстрок (Loki сводит альтернативу литералов к contains без
	// регэкспа), строгий регэксп — только по оставшимся строкам
	filtered := u.conf.ClusterSelector + ` |~ "(?i)err|fatal|panic|crit"` + " |~ " + strconv.Quote(errorLineRe)
	countQuery := fmt.Sprintf("sum by (%s) (count_over_time(%s [%ds]))",
		strings.Join(append(append([]string{}, namespaceLabels...), podLabels...), ", "), filtered, int(win.Seconds()))

	var samples []logsModel.Line
	var counts map[string]*logsModel.ServiceErrors

	eg, egCtx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		vector, err := u.loki.QueryVector(egCtx, countQuery, end)
		if err != nil {
			return fmt.Errorf("loki.QueryVector: %w", err)
		}
		counts = make(map[string]*logsModel.ServiceErrors, len(vector))
		for _, s := range vector {
			owner := logsModel.Line{}
			owners.attach(s.Labels, &owner)
			key := owner.Service + "\x00" + owner.Namespace
			if counts[key] == nil {
				counts[key] = &logsModel.ServiceErrors{Service: owner.Service, Namespace: owner.Namespace}
			}
			counts[key].Count += int(s.Value)
		}
		return nil
	})
	// выборка — только уточнение (текст ошибки): без неё счётчики всё равно верны
	eg.Go(func() error {
		lines, err := u.fetch(egCtx, filtered, "", end.Add(-win), end, u.conf.MaxLines, owners.attach)
		if err != nil {
			slog.Warn("logs: cluster errors sample", "error", err)
			return nil
		}
		samples = lines
		return nil
	})
	if err = eg.Wait(); err != nil {
		return nil, err
	}

	services := lo.Filter(lo.Values(counts), func(s *logsModel.ServiceErrors, _ int) bool { return s.Count > 0 })
	sort.Slice(services, func(i, j int) bool {
		if services[i].Count != services[j].Count {
			return services[i].Count > services[j].Count
		}
		return services[i].Service+services[i].Namespace < services[j].Service+services[j].Namespace
	})

	result := &logsModel.ClusterErrors{
		Window:        win,
		Total:         lo.SumBy(services, func(s *logsModel.ServiceErrors) int { return s.Count }),
		ServicesTotal: len(services),
	}
	if top > 0 && len(services) > top {
		services = services[:top]
	}

	byOwner := lo.GroupBy(samples, func(l logsModel.Line) string { return l.Service + "\x00" + l.Namespace })
	result.Services = lo.Map(services, func(s *logsModel.ServiceErrors, _ int) logsModel.ServiceErrors {
		if patterns := u.patterns.Aggregate(byOwner[s.Service+"\x00"+s.Namespace], 1); len(patterns) > 0 {
			s.Top = patterns[0]
		}
		return *s
	})

	return result, nil
}

// owners — чей под: workload'ы каталога по namespace'ам.
type owners map[string][]*workloadModel.Main

func (u *Usecase) owners(ctx context.Context) (owners, error) {
	workloads, _, err := u.workload.List(ctx, &workloadModel.ListReq{})
	if err != nil {
		return nil, fmt.Errorf("workload.List: %w", err)
	}
	return lo.GroupBy(workloads, func(w *workloadModel.Main) string { return w.Namespace }), nil
}

// attach — владелец строки по лейблам потока: workload каталога по правилам именования
// подов его вида (podname.Owner: sms-im-7d9f8b6c5-q2x4z → sms-im, а не sms; у семейства
// Job'ов имя — общий префикс). Под не из каталога — только namespace.
func (o owners) attach(labels map[string]string, line *logsModel.Line) {
	line.Namespace = namespaceOf(labels)
	pod := podOf(labels)

	workloads := o[line.Namespace]
	i := podname.Owner(pod, lo.Map(workloads, func(w *workloadModel.Main, _ int) podname.Workload {
		return podname.Workload{Kind: w.Kind, Name: w.Name}
	}))
	if i >= 0 {
		line.Service, line.Workload = workloads[i].ServiceName, workloads[i].Name
	}
}
