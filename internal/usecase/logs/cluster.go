package logs

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	logsModel "github.com/mechta-market/pulse/internal/domain/logs/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	"github.com/mechta-market/pulse/internal/errs"
	"github.com/mechta-market/pulse/internal/usecase/logs/model"
)

// minSearchLen — короче искать по всем логам кластера бессмысленно: совпадёт почти всё.
const minSearchLen = 3

// search — поиск текста (номер заказа, id клиента) во всех логах кластера: строки
// привязываются к сервисам каталога по поду, в ответе — где и сколько нашлось.
// Только Loki: обойти логи всех подов через Kubernetes API за разумное время нельзя.
func (u *Usecase) search(ctx context.Context, req *model.QueryReq, level, mode string, win time.Duration, limit int) (*model.QueryResult, error) {
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

	owners, err := u.owners(ctx)
	if err != nil {
		return nil, err
	}

	end := time.Now().UTC()
	start := end.Add(-win)
	lines, err := u.fetch(ctx, logQL(u.conf.ClusterSelector, pattern, level), level, start, end, limit, owners.attach)
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
		Truncated:  len(lines) >= limit,
		Services:   serviceHits(lines),
	}

	if mode == model.ModeRaw {
		result.Lines = lines
		return result, nil
	}

	// workload'ы разных сервисов в одном паттерне ничего не добавляют к списку сервисов
	result.Patterns = lo.Map(u.patterns.Aggregate(lines, u.conf.MaxPatterns), func(p logsModel.Pattern, _ int) logsModel.Pattern {
		p.Workloads = nil
		return p
	})
	return result, nil
}

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
// внутри обёртки сборщика, где кавычки экранированы) или словом ERROR/FATAL/PANIC капсом.
const errorLineRe = `(?i:(?:level|lvl|severity)\\?"?\s*[:=]\s*\\?"?(?:error|err|fatal|panic|crit|critical)\b)|\b(?:ERROR|FATAL|PANIC|CRITICAL)\b|\bpanic: `

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
	filtered := u.conf.ClusterSelector + " |~ " + strconv.Quote(errorLineRe)
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

// attach — владелец строки по лейблам потока: workload каталога с самым длинным префиксом
// имени пода (sms-im-7d9f-q2 → sms-im, а не sms; у семейства Job'ов имя — общий префикс).
// Под не из каталога — только namespace.
func (o owners) attach(labels map[string]string, line *logsModel.Line) {
	line.Namespace = namespaceOf(labels)
	pod := podOf(labels)

	var best *workloadModel.Main
	for _, w := range o[line.Namespace] {
		if strings.HasPrefix(pod, w.Name+"-") && (best == nil || len(w.Name) > len(best.Name)) {
			best = w
		}
	}
	if best != nil {
		line.Service, line.Workload = best.ServiceName, best.Name
	}
}
