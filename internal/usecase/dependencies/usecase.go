// Package dependencies — граф сконфигурированных связей (env/configmap, позже kusec).
// Важно: это связи по конфигурации, а не фактический трафик — см. описание инструмента.
package dependencies

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	"github.com/rendau/pulse/internal/constant"
	dependencyModel "github.com/rendau/pulse/internal/domain/dependency/model"
	snapshotModel "github.com/rendau/pulse/internal/domain/snapshot/model"
	svcModel "github.com/rendau/pulse/internal/domain/svc/model"
	workloadModel "github.com/rendau/pulse/internal/domain/workload/model"
	"github.com/rendau/pulse/internal/errs"
	k8sModel "github.com/rendau/pulse/internal/service/k8s/model"
	"github.com/rendau/pulse/internal/usecase/dependencies/model"
)

// Config — лимиты (из yaml-правил).
type Config struct {
	MaxNodes int
	MaxDepth int
	// HealthDeadline — общий дедлайн на живые запросы состояния соседей
	HealthDeadline time.Duration
}

type Usecase struct {
	conf Config

	svc      svcServiceI
	workload workloadServiceI
	depend   dependencyServiceI
	k8s      k8sClientI
	rules    rulesServiceI
}

func New(conf Config, svc svcServiceI, workload workloadServiceI, depend dependencyServiceI, k8s k8sClientI, rules rulesServiceI) *Usecase {
	if conf.MaxNodes <= 0 {
		conf.MaxNodes = 50
	}
	if conf.MaxDepth <= 0 {
		conf.MaxDepth = 3
	}
	if conf.HealthDeadline <= 0 {
		conf.HealthDeadline = 5 * time.Second
	}
	return &Usecase{conf: conf, svc: svc, workload: workload, depend: depend, k8s: k8s, rules: rules}
}

func (u *Usecase) Graph(ctx context.Context, req *model.GraphReq) (*model.Graph, error) {
	direction := lo.CoalesceOrEmpty(req.Direction, model.DirectionBoth)
	if !lo.Contains([]string{model.DirectionUpstream, model.DirectionDownstream, model.DirectionBoth}, direction) {
		return nil, fmt.Errorf("%w: direction %q; expected upstream | downstream | both", errs.InvalidRequest, req.Direction)
	}
	depth := req.Depth
	if depth <= 0 {
		depth = 1
	}
	if depth > u.conf.MaxDepth {
		return nil, fmt.Errorf("%w: depth %d exceeds maximum %d", errs.InvalidRequest, depth, u.conf.MaxDepth)
	}

	root, err := u.svc.GetOrSuggest(ctx, req.Service)
	if err != nil {
		return nil, fmt.Errorf("svc.GetOrSuggest: %w", err)
	}

	// весь граф кластера невелик (сотни рёбер): читаем целиком и обходим в памяти
	all, _, err := u.depend.List(ctx, &dependencyModel.ListReq{})
	if err != nil {
		return nil, fmt.Errorf("depend.List: %w", err)
	}

	graph := &model.Graph{Service: root.Name, Direction: direction, Depth: depth}
	walk := newWalker(all, u.conf.MaxNodes)
	walk.run(root.Name, direction, depth)
	graph.Nodes, graph.Edges, graph.Truncated = walk.result()

	if err = u.enrich(ctx, graph); err != nil {
		graph.Errors = append(graph.Errors, model.SourceError{Source: constant.SourceK8s, Message: err.Error()})
	}

	return graph, nil
}

// enrich добавляет узлам title и краткий статус здоровья (по подам, без полного снапшота).
func (u *Usecase) enrich(ctx context.Context, graph *model.Graph) error {
	names := lo.FilterMap(graph.Nodes, func(n model.Node, _ int) (string, bool) { return n.Name, !n.External })
	if len(names) == 0 {
		return nil
	}

	services, _, err := u.svc.List(ctx, &svcModel.ListReq{Names: names})
	if err != nil {
		return fmt.Errorf("svc.List: %w", err)
	}
	titles := lo.SliceToMap(services, func(s *svcModel.Main) (string, string) { return s.Name, s.Title })

	workloads, _, err := u.workload.List(ctx, &workloadModel.ListReq{ServiceNames: names})
	if err != nil {
		return fmt.Errorf("workload.List: %w", err)
	}
	byService := lo.GroupBy(workloads, func(w *workloadModel.Main) string { return w.ServiceName })

	ctx, cancel := context.WithTimeout(ctx, u.conf.HealthDeadline)
	defer cancel()

	var mu sync.Mutex
	var firstErr error
	health := make(map[string]string, len(names))
	podsInfo := make(map[string]string, len(names))

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(8)
	for _, name := range names {
		eg.Go(func() error {
			snap := &snapshotModel.Snapshot{Service: name}
			var ready, total int
			var unavailable bool
			for _, w := range byService[name] {
				state := snapshotModel.WorkloadState{Namespace: w.Namespace, Kind: w.Kind, Name: w.Name, ReplicasDesired: w.ReplicasDesired}
				if w.Selector != "" {
					pods, err := u.k8s.ListPods(egCtx, w.Namespace, w.Selector)
					if err != nil {
						unavailable = true
						mu.Lock()
						if firstErr == nil {
							firstErr = err
						}
						mu.Unlock()
					} else {
						state.Pods = podsState(pods)
						ready += state.Pods.Ready
						total += state.Pods.Total
					}
				}
				snap.Workloads = append(snap.Workloads, state)
			}
			mu.Lock()
			health[name] = u.rules.ComputeHealth(snap, unavailable)
			if total > 0 {
				podsInfo[name] = fmt.Sprintf("%d/%d ready", ready, total)
			}
			mu.Unlock()
			return nil
		})
	}
	_ = eg.Wait()

	for i := range graph.Nodes {
		n := &graph.Nodes[i]
		if n.External {
			continue
		}
		n.Title = titles[n.Name]
		n.Health = health[n.Name]
		n.PodsInfo = podsInfo[n.Name]
	}

	return firstErr
}

func podsState(pods []k8sModel.Pod) snapshotModel.PodsState {
	state := snapshotModel.PodsState{Total: len(pods)}
	for _, pod := range pods {
		if pod.Ready {
			state.Ready++
		}
		for _, c := range pod.Containers {
			if c.State != "running" && c.Reason != "" && c.Reason != "Completed" {
				state.Problems = append(state.Problems, snapshotModel.PodProblem{Pod: pod.Name, Container: c.Name, Reason: c.Reason})
			}
		}
	}
	return state
}

// walker — обход графа в ширину с лимитом узлов.
type walker struct {
	out, in  map[string][]*dependencyModel.Main // from → рёбра; to → рёбра
	maxNodes int

	distance  map[string]int
	external  map[string]struct{}
	edges     map[string]*model.Edge
	truncated bool
}

func newWalker(all []*dependencyModel.Main, maxNodes int) *walker {
	w := &walker{
		out: make(map[string][]*dependencyModel.Main), in: make(map[string][]*dependencyModel.Main),
		maxNodes: maxNodes, distance: make(map[string]int), external: make(map[string]struct{}), edges: make(map[string]*model.Edge),
	}
	for _, d := range all {
		w.out[d.FromService] = append(w.out[d.FromService], d)
		if d.ToService != "" {
			w.in[d.ToService] = append(w.in[d.ToService], d)
		}
	}
	return w
}

func (w *walker) run(root, direction string, depth int) {
	w.distance[root] = 0
	frontier := []string{root}

	for level := 1; level <= depth && len(frontier) > 0; level++ {
		next := make([]string, 0)
		for _, name := range frontier {
			if direction != model.DirectionDownstream {
				for _, d := range w.out[name] {
					w.addEdge(d)
					target := d.ToService
					if target == "" {
						target = externalNode(d.ToHost)
						w.external[target] = struct{}{}
					}
					if w.visit(target, level) && d.ToService != "" {
						next = append(next, target)
					}
				}
			}
			if direction != model.DirectionUpstream {
				for _, d := range w.in[name] {
					w.addEdge(d)
					if w.visit(d.FromService, level) {
						next = append(next, d.FromService)
					}
				}
			}
		}
		frontier = next
	}
}

func (w *walker) visit(name string, level int) bool {
	if _, seen := w.distance[name]; seen {
		return false
	}
	if len(w.distance) >= w.maxNodes {
		w.truncated = true
		return false
	}
	w.distance[name] = level
	return true
}

func (w *walker) addEdge(d *dependencyModel.Main) {
	to := d.ToService
	if to == "" {
		to = externalNode(d.ToHost)
	}
	key := fmt.Sprintf("%s|%s|%s|%d", d.FromService, to, d.ToHost, d.Port)
	edge, ok := w.edges[key]
	if !ok {
		edge = &model.Edge{From: d.FromService, To: to, Host: d.ToHost, Port: d.Port, Scheme: d.Scheme, Source: d.Source, LastSeen: d.LastSeen}
		w.edges[key] = edge
	}
	edge.Keys = lo.Uniq(append(edge.Keys, d.Key))
	if d.LastSeen.After(edge.LastSeen) {
		edge.LastSeen = d.LastSeen
	}
}

func (w *walker) result() ([]model.Node, []model.Edge, bool) {
	nodes := make([]model.Node, 0, len(w.distance))
	for name, dist := range w.distance {
		_, external := w.external[name]
		nodes = append(nodes, model.Node{Name: name, External: external, Distance: dist})
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Distance != nodes[j].Distance {
			return nodes[i].Distance < nodes[j].Distance
		}
		return nodes[i].Name < nodes[j].Name
	})

	edges := make([]model.Edge, 0, len(w.edges))
	for _, e := range w.edges {
		// ребро попадает в ответ, только если оба конца — узлы графа (лимит узлов мог отсечь цель)
		if _, ok := w.distance[e.From]; !ok {
			continue
		}
		if _, ok := w.distance[e.To]; !ok {
			continue
		}
		sort.Strings(e.Keys)
		edges = append(edges, *e)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})

	return nodes, edges, w.truncated
}

// externalNode — имя узла для внешнего адреса.
func externalNode(host string) string {
	return "external:" + host
}
