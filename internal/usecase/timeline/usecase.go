// Package timeline — «что изменилось перед тем, как сломалось»: деплои, коммиты, изменения
// конфигурации, срабатывания алертов, масштабирование и рестарты на одной оси времени.
package timeline

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	"github.com/mechta-market/pulse/internal/constant"
	deployModel "github.com/mechta-market/pulse/internal/domain/deploy/model"
	eventModel "github.com/mechta-market/pulse/internal/domain/event/model"
	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	"github.com/mechta-market/pulse/internal/errs"
	githubModel "github.com/mechta-market/pulse/internal/service/github/model"
	"github.com/mechta-market/pulse/internal/usecase/timeline/model"
)

// Config — лимиты (из yaml-правил).
type Config struct {
	Deadline     time.Duration
	MaxEvents    int
	CommitsLimit int
	// MaxServicesForCommits — выше этого числа сервисов коммиты в таймлайн не собираются
	// (по запросу на репозиторий); scope=cluster всегда без коммитов
	MaxServicesForCommits int
}

type Usecase struct {
	conf Config

	svc        svcServiceI
	workload   workloadServiceI
	deploy     deployServiceI
	k8s        k8sClientI
	github     githubClientI
	kusec      KusecI
	prometheus PrometheusI
	events     eventServiceI
	rules      rulesServiceI
}

func New(
	conf Config,
	svc svcServiceI,
	workload workloadServiceI,
	deploy deployServiceI,
	k8s k8sClientI,
	github githubClientI,
	kusec KusecI,
	prometheus PrometheusI,
	events eventServiceI,
	rules rulesServiceI,
) *Usecase {
	if conf.Deadline <= 0 {
		conf.Deadline = 8 * time.Second
	}
	if conf.MaxEvents <= 0 {
		conf.MaxEvents = 100
	}
	if conf.CommitsLimit <= 0 {
		conf.CommitsLimit = 100
	}
	if conf.MaxServicesForCommits <= 0 {
		conf.MaxServicesForCommits = 10
	}
	return &Usecase{
		conf: conf, svc: svc, workload: workload, deploy: deploy, k8s: k8s,
		github: github, kusec: kusec, prometheus: prometheus, events: events, rules: rules,
	}
}

// Timeline собирает изменения по сервисам (или по всему кластеру) на одной оси времени,
// новые первыми. Ошибка источника — в errors, а не отказ.
func (u *Usecase) Timeline(ctx context.Context, req *model.TimelineReq) (*model.TimelineResult, error) {
	win := req.Window
	if win <= 0 {
		win = 24 * time.Hour
	}

	services, err := u.resolveServices(ctx, req)
	if err != nil {
		return nil, err
	}
	names := lo.Map(services, func(s *svcModel.Main, _ int) string { return s.Name })

	workloads, _, err := u.workload.List(ctx, &workloadModel.ListReq{ServiceNames: names})
	if err != nil {
		return nil, fmt.Errorf("workload.List: %w", err)
	}

	now := time.Now().UTC()
	c := &collector{u: u, services: services, workloads: workloads, since: now.Add(-win), now: now}
	clusterScope := req.Scope == model.ScopeCluster

	ctx, cancel := context.WithTimeout(ctx, u.conf.Deadline)
	defer cancel()

	eg, egCtx := errgroup.WithContext(ctx)
	eg.Go(func() error { c.deploys(egCtx); return nil })
	eg.Go(func() error { c.clusterEvents(egCtx, clusterScope); return nil })
	eg.Go(func() error { c.alertHistory(egCtx, clusterScope); return nil })
	withDetails := !clusterScope && len(services) <= u.conf.MaxServicesForCommits
	eg.Go(func() error { c.configuration(egCtx, withDetails); return nil })
	if withDetails {
		eg.Go(func() error { c.commits(egCtx); return nil })
	}
	_ = eg.Wait()

	sort.SliceStable(c.events, func(i, j int) bool { return c.events[i].TS.After(c.events[j].TS) })

	// по кластеру — только сервисы с событиями за окно, а не весь каталог
	if clusterScope {
		names = lo.Uniq(lo.FilterMap(c.events, func(e eventModel.Event, _ int) (string, bool) { return e.Service, e.Service != "" }))
	}

	result := &model.TimelineResult{
		Services:   names,
		Window:     win,
		Events:     c.events,
		TotalCount: len(c.events),
		Errors:     lo.UniqBy(c.errors, func(e model.SourceError) string { return e.Source }),
	}
	if len(result.Events) > u.conf.MaxEvents {
		result.Events = result.Events[:u.conf.MaxEvents]
		result.Truncated = true
	}

	return result, nil
}

// Changes — детализация по одному сервису: коммиты, что не в проде, деплои, конфигурация.
func (u *Usecase) Changes(ctx context.Context, serviceName string, win time.Duration) (*model.ChangesResult, error) {
	if win <= 0 {
		win = 24 * time.Hour
	}

	service, err := u.svc.GetOrSuggest(ctx, serviceName)
	if err != nil {
		return nil, fmt.Errorf("svc.GetOrSuggest: %w", err)
	}
	workloads, _, err := u.workload.List(ctx, &workloadModel.ListReq{ServiceName: new(service.Name)})
	if err != nil {
		return nil, fmt.Errorf("workload.List: %w", err)
	}

	now := time.Now().UTC()
	since := now.Add(-win)
	result := &model.ChangesResult{Service: service.Name, Window: win}
	var mu sync.Mutex
	addError := func(source string, err error) {
		mu.Lock()
		defer mu.Unlock()
		result.Errors = append(result.Errors, model.SourceError{Source: source, Message: compactError(err)})
	}

	ctx, cancel := context.WithTimeout(ctx, u.conf.Deadline)
	defer cancel()

	eg, egCtx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		deploys, _, err := u.deploy.List(egCtx, &deployModel.ListReq{ServiceNames: []string{service.Name}, Since: &since})
		if err != nil {
			addError(constant.SourcePostgres, err)
			return nil
		}
		mu.Lock()
		result.Deploys = deploys
		mu.Unlock()
		return nil
	})

	if service.RepoUrl != "" {
		eg.Go(func() error {
			commits, err := u.github.ListCommits(egCtx, service.RepoUrl, since, now, u.conf.CommitsLimit)
			if err != nil {
				addError(constant.SourceGithub, err)
				return nil
			}
			// за окно коммитов нет — последний коммит ветки, каким бы старым он ни был:
			// «о чём последний коммит?» не должно упираться в окно
			var last []githubModel.Commit
			if len(commits) == 0 {
				if last, err = u.github.ListCommits(egCtx, service.RepoUrl, time.Time{}, now, 1); err != nil {
					addError(constant.SourceGithub, err)
				}
			}
			mu.Lock()
			result.Commits = lo.Map(commits, encodeCommit)
			if len(last) > 0 {
				result.LastCommit = new(encodeCommit(last[0], 0))
			}
			mu.Unlock()
			return nil
		})

		// «в проде отстаёт на N коммитов»: сравнение задеплоенного SHA с веткой по умолчанию
		deployed := lo.Uniq(lo.FilterMap(workloads, func(w *workloadModel.Main, _ int) (string, bool) {
			return w.DeployedCommit, w.DeployedCommit != ""
		}))
		if len(deployed) == 1 {
			eg.Go(func() error {
				cmp, err := u.github.CompareCommits(egCtx, service.RepoUrl, deployed[0])
				if err != nil {
					addError(constant.SourceGithub, err)
					return nil
				}
				mu.Lock()
				result.Unreleased = &model.Unreleased{DeployedCommit: deployed[0], BehindBy: cmp.AheadBy, Commits: lo.Map(cmp.Commits, encodeCommit)}
				mu.Unlock()
				return nil
			})
		}
	}

	eg.Go(func() error {
		config := u.configuration(egCtx, workloads, since, now, true, true, addError)
		mu.Lock()
		result.ConfigChanges = config.changes()
		if u.kusec != nil {
			result.UnsyncedConfig = lo.CoalesceSliceOrEmpty(config.unsynced)
		}
		mu.Unlock()
		return nil
	})

	_ = eg.Wait()

	sort.SliceStable(result.ConfigChanges, func(i, j int) bool { return result.ConfigChanges[i].TS.After(result.ConfigChanges[j].TS) })
	result.Errors = lo.UniqBy(result.Errors, func(e model.SourceError) string { return e.Source })
	return result, nil
}

func (u *Usecase) resolveServices(ctx context.Context, req *model.TimelineReq) ([]*svcModel.Main, error) {
	if req.Scope == model.ScopeCluster {
		services, _, err := u.svc.List(ctx, &svcModel.ListReq{})
		if err != nil {
			return nil, fmt.Errorf("svc.List: %w", err)
		}
		return services, nil
	}
	if req.Scope != "" {
		return nil, fmt.Errorf("%w: scope %q; expected empty or %q", errs.InvalidRequest, req.Scope, model.ScopeCluster)
	}

	names := lo.Uniq(lo.Filter(req.Services, func(s string, _ int) bool { return strings.TrimSpace(s) != "" }))
	if len(names) == 0 {
		return nil, fmt.Errorf("%w: service (or services, or scope=cluster) is required", errs.InvalidRequest)
	}

	services := make([]*svcModel.Main, 0, len(names))
	for _, name := range names {
		service, err := u.svc.GetOrSuggest(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("svc.GetOrSuggest: %w", err)
		}
		services = append(services, service)
	}
	return services, nil
}

var urlRe = regexp.MustCompile(`https?://[^\s"]+`)

func compactError(err error) string {
	return urlRe.ReplaceAllString(err.Error(), "<url>")
}
