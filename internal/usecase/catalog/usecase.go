package catalog

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	"github.com/rendau/pulse/internal/constant"
	svcModel "github.com/rendau/pulse/internal/domain/svc/model"
	svcService "github.com/rendau/pulse/internal/domain/svc/service"
	workloadModel "github.com/rendau/pulse/internal/domain/workload/model"
	"github.com/rendau/pulse/internal/errs"
	k8sModel "github.com/rendau/pulse/internal/service/k8s/model"
	"github.com/rendau/pulse/internal/usecase/catalog/model"
	"github.com/rendau/pulse/internal/util/tz"
)

const (
	defaultPageSize = 100
	maxPageSize     = 500
	// liveTimeout — общий дедлайн на живые запросы к кластеру внутри одного вызова
	liveTimeout     = 5 * time.Second
	liveConcurrency = 5
)

type Usecase struct {
	svc      svcServiceI
	workload workloadServiceI
	k8s      k8sClientI
}

func New(svc svcServiceI, workload workloadServiceI, k8s k8sClientI) *Usecase {
	return &Usecase{svc: svc, workload: workload, k8s: k8s}
}

func (u *Usecase) Resolve(ctx context.Context, query string) (*model.ResolveResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("%w: query is required", errs.InvalidRequest)
	}

	candidates, err := u.svc.Resolve(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("svc.Resolve: %w", err)
	}

	namespaces, err := u.namespacesByService(ctx, lo.Map(candidates, func(c *svcModel.Candidate, _ int) string { return c.Service.Name }))
	if err != nil {
		return nil, err
	}

	return &model.ResolveResult{
		Ambiguous: svcService.Ambiguous(candidates),
		Candidates: lo.Map(candidates, func(c *svcModel.Candidate, _ int) *model.Candidate {
			return &model.Candidate{
				Service:    c.Service,
				Confidence: c.Confidence,
				MatchedBy:  c.MatchedBy,
				Namespaces: namespaces[c.Service.Name],
			}
		}),
	}, nil
}

func (u *Usecase) List(ctx context.Context, pars *model.ListReq) ([]*model.ServiceSummary, int64, error) {
	if pars == nil {
		pars = &model.ListReq{}
	}
	if pars.PageSize <= 0 {
		pars.PageSize = defaultPageSize
	}
	if pars.PageSize > maxPageSize {
		return nil, 0, fmt.Errorf("%w: page_size must be <= %d", errs.IncorrectPageSize, maxPageSize)
	}
	if pars.Page < 0 {
		return nil, 0, fmt.Errorf("%w: page must be >= 0", errs.InvalidRequest)
	}

	req := &svcModel.ListReq{
		Team:        pars.Team,
		Namespace:   pars.Namespace,
		Criticality: pars.Criticality,
		HasMetadata: pars.HasMetadata,
		Search:      pars.Search,
	}
	req.Page, req.PageSize, req.WithTotalCount = pars.Page, pars.PageSize, true

	services, total, err := u.svc.List(ctx, req)
	if err != nil {
		return nil, 0, fmt.Errorf("svc.List: %w", err)
	}

	names := lo.Map(services, func(s *svcModel.Main, _ int) string { return s.Name })
	workloads, err := u.workloadsByService(ctx, names)
	if err != nil {
		return nil, 0, err
	}

	return lo.Map(services, func(s *svcModel.Main, _ int) *model.ServiceSummary {
		return &model.ServiceSummary{Service: s, Namespaces: namespacesOf(workloads[s.Name]), Manifest: bestManifest(workloads[s.Name])}
	}), total, nil
}

func (u *Usecase) Info(ctx context.Context, name string) (*model.ServiceInfo, error) {
	service, err := u.svc.GetOrSuggest(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("svc.GetOrSuggest: %w", err)
	}

	workloads, _, err := u.workload.List(ctx, &workloadModel.ListReq{ServiceName: new(service.Name)})
	if err != nil {
		return nil, fmt.Errorf("workload.List: %w", err)
	}

	result := &model.ServiceInfo{
		Service: service,
		Workloads: lo.Map(workloads, func(w *workloadModel.Main, _ int) *model.WorkloadInfo {
			return &model.WorkloadInfo{Workload: w}
		}),
	}

	// живое состояние подов: параллельно, с общим дедлайном; ошибка кластера — в errors
	liveCtx, cancel := context.WithTimeout(ctx, liveTimeout)
	defer cancel()

	eg, egCtx := errgroup.WithContext(liveCtx)
	eg.SetLimit(liveConcurrency)
	for _, info := range result.Workloads {
		if info.Workload.Selector == "" {
			continue
		}
		eg.Go(func() error {
			pods, err := u.k8s.ListPods(egCtx, info.Workload.Namespace, info.Workload.Selector)
			if err != nil {
				return fmt.Errorf("%s/%s: %w", info.Workload.Namespace, info.Workload.Name, err)
			}
			info.Pods = podsState(pods)
			return nil
		})
	}
	if err = eg.Wait(); err != nil {
		result.Errors = append(result.Errors, model.SourceError{Source: constant.SourceK8s, Message: err.Error()})
	}

	return result, nil
}

func (u *Usecase) namespacesByService(ctx context.Context, names []string) (map[string][]string, error) {
	workloads, err := u.workloadsByService(ctx, names)
	if err != nil {
		return nil, err
	}
	return lo.MapValues(workloads, func(list []*workloadModel.Main, _ string) []string { return namespacesOf(list) }), nil
}

func (u *Usecase) workloadsByService(ctx context.Context, names []string) (map[string][]*workloadModel.Main, error) {
	if len(names) == 0 {
		return map[string][]*workloadModel.Main{}, nil
	}

	workloads, _, err := u.workload.List(ctx, &workloadModel.ListReq{ServiceNames: names})
	if err != nil {
		return nil, fmt.Errorf("workload.List: %w", err)
	}

	return lo.GroupBy(workloads, func(w *workloadModel.Main) string { return w.ServiceName }), nil
}

func namespacesOf(workloads []*workloadModel.Main) []string {
	namespaces := lo.Uniq(lo.Map(workloads, func(w *workloadModel.Main, _ int) string { return w.Namespace }))
	sort.Strings(namespaces)
	return namespaces
}

// manifestRank — чем меньше, тем лучше статус манифеста.
var manifestRank = map[string]int{
	workloadModel.ManifestOk: 1, workloadModel.ManifestPartial: 2, workloadModel.ManifestInvalid: 3,
	workloadModel.ManifestAbsent: 4, workloadModel.ManifestUnreachable: 5,
}

// bestManifest — лучший статус манифеста среди workload'ов сервиса; пусто — не искали.
func bestManifest(workloads []*workloadModel.Main) string {
	best := ""
	for _, w := range workloads {
		rank, ok := manifestRank[w.Manifest.Status]
		if ok && (best == "" || rank < manifestRank[best]) {
			best = w.Manifest.Status
		}
	}
	return best
}

// podsState сводит поды к счётчикам и списку проблем в человекочитаемом виде.
func podsState(pods []k8sModel.Pod) *model.PodsState {
	state := &model.PodsState{Total: len(pods)}

	for _, pod := range pods {
		if pod.Ready {
			state.Ready++
		}
		state.Restarts += pod.Restarts

		if pod.Phase == "Pending" || pod.Phase == "Failed" {
			state.Problems = append(state.Problems, fmt.Sprintf("%s: %s", pod.Name, pod.Phase))
		}
		for _, c := range pod.Containers {
			if c.State != "running" && c.Reason != "" && c.Reason != "Completed" {
				state.Problems = append(state.Problems, fmt.Sprintf("%s/%s: %s", pod.Name, c.Name, c.Reason))
			}
			if c.LastTerminationReason == "OOMKilled" {
				state.Problems = append(state.Problems, fmt.Sprintf("%s/%s: last termination OOMKilled at %s",
					pod.Name, c.Name, tz.In(c.LastTerminatedAt).Format(time.RFC3339)))
			}
		}
	}

	return state
}
