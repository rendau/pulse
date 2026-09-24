package logs

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	logsModel "github.com/mechta-market/pulse/internal/domain/logs/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
	"github.com/mechta-market/pulse/internal/util/redact"
)

const (
	// maxK8sPods — сколько подов читать напрямую (самые свежие): каждый контейнер — запрос к API
	maxK8sPods = 20
	// k8sLogsConcurrency — параллельных запросов pods/log: не нагружать API-сервер и kubelet
	k8sLogsConcurrency = 5
)

// fetchK8s — логи из Kubernetes API (как kubectl logs): запасной источник, когда Loki не
// подключён или недоступен. Только живые поды сервиса (удалённых на нодах уже нет), хвост
// каждого контейнера; у перезапускавшихся в окне — и прошлый запуск (--previous).
// Фильтры уровня и регэкспа — на стороне pulse; PII маскируется так же, как у Loki.
func (u *Usecase) fetchK8s(ctx context.Context, groups []podGroup, pattern, level string, start, end time.Time, limit int) ([]logsModel.Line, error) {
	var re *regexp.Regexp
	if pattern != "" {
		var err error
		if re, err = regexp.Compile(pattern); err != nil {
			return nil, fmt.Errorf("pattern: %w", err)
		}
	}

	pods, err := u.k8s.ListPods(ctx, groups[0].Namespace, "")
	if err != nil {
		return nil, fmt.Errorf("k8s.ListPods: %w", err)
	}
	pods = lo.Filter(pods, func(p k8sModel.Pod, _ int) bool { return groupOfPod(p.Name, groups) != "" })
	sort.Slice(pods, func(i, j int) bool { return pods[i].StartedAt.After(pods[j].StartedAt) })
	if len(pods) > maxK8sPods {
		pods = pods[:maxK8sPods]
	}

	type target struct {
		pod, container, workload string
		previous                 bool
	}
	targets := make([]target, 0, len(pods))
	for _, pod := range pods {
		workload := groupOfPod(pod.Name, groups)
		for _, ct := range pod.Containers {
			targets = append(targets, target{pod: pod.Name, container: ct.Name, workload: workload})
			if ct.Restarts > 0 && ct.LastTerminatedAt.After(start) {
				targets = append(targets, target{pod: pod.Name, container: ct.Name, workload: workload, previous: true})
			}
		}
	}
	if len(targets) == 0 {
		return []logsModel.Line{}, nil
	}

	var (
		mu     sync.Mutex
		lines  = make([]logsModel.Line, 0, 256)
		failed []error
	)
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(k8sLogsConcurrency)
	for _, t := range targets {
		eg.Go(func() error {
			entries, err := u.k8s.PodLogs(egCtx, groups[0].Namespace, t.pod, t.container, start, int64(limit), t.previous)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed = append(failed, err)
				return nil
			}
			for _, e := range entries {
				if e.TS.After(end) || (re != nil && !re.MatchString(e.Text)) {
					continue
				}
				line := logsModel.Line{TS: e.TS, Text: redact.Text(e.Text), Level: u.patterns.DetectLevel(e.Text), Workload: t.workload}
				if level != "" && line.Level != level {
					continue
				}
				lines = append(lines, line)
			}
			return nil
		})
	}
	_ = eg.Wait()

	if len(failed) == len(targets) {
		return nil, fmt.Errorf("k8s.PodLogs: %w", errors.Join(failed...))
	}

	sort.SliceStable(lines, func(i, j int) bool { return lines[i].TS.After(lines[j].TS) })
	if len(lines) > limit {
		lines = lines[:limit]
	}
	return lines, nil
}
