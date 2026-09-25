package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/mechta-market/pulse/internal/constant"
	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	localModel "github.com/mechta-market/pulse/internal/service/indexer/service/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
	svcproxyModel "github.com/mechta-market/pulse/internal/service/svcproxy/model"
)

// Поиск манифеста сервиса на подах (docs/service-manifest.md, «Как pulse находит манифест»).

const (
	manifestConcurrency = 8
	// таймаут на порт: известные кандидаты — 2 с, перебор остальных портов — 1 с
	probeTimeout         = 2 * time.Second
	fallbackProbeTimeout = time.Second
)

// probeManifests ищет манифест у workload'ов, которым пора: сменился образ, принятый манифест
// старше RefreshAfter, неудача старше RetryAfter. Остальным — прошлый результат из каталога.
// Итог — в draft.manifest (действующий манифест) и draft.manifestProbe (новый результат поиска).
func (s *Service) probeManifests(ctx context.Context, drafts []*workloadDraft, pods []k8sModel.Pod, now time.Time) {
	if s.pods == nil || s.conf.Manifest.Path == "" {
		return
	}

	previous, _, err := s.workload.List(ctx, &workloadModel.ListReq{Cluster: new(s.conf.Cluster)})
	if err != nil {
		slog.Warn("indexer: previous workloads are unavailable, manifests will not be probed", "error", err)
		return
	}
	byKey := lo.SliceToMap(previous, func(w *workloadModel.Main) (workloadModel.Key, *workloadModel.Main) { return w.Key(), w })

	scrapePorts := s.scrapePorts(ctx)

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(manifestConcurrency)
	for _, d := range drafts {
		prev := byKey[workloadModel.Key{Cluster: s.conf.Cluster, Namespace: d.Namespace, Kind: d.Kind, Name: d.Name}]
		var prevManifest *workloadModel.Manifest
		if prev != nil && prev.Manifest.Status != "" {
			prevManifest = &prev.Manifest
		}

		ready := readyPods(pods, d)
		if len(ready) == 0 || !s.probeDue(prevManifest, d.digest, now) {
			// прошлый результат: манифест разбирается заново — правила проверки могли поменяться
			if prevManifest != nil {
				d.manifest = parseStored(prevManifest)
				// коммит из манифеста — только того же образа
				if d.manifest != nil && prevManifest.Digest != d.digest {
					d.manifest.Commit = ""
				}
			}
			continue
		}

		// перебор остальных портов пода — только раз после выкатки (или при первом поиске)
		deployed := prevManifest == nil || prevManifest.Digest != d.digest
		eg.Go(func() error {
			probe, parsed := s.probe(egCtx, ready[0], scrapePorts, deployed)
			probe.Digest, probe.CheckedAt = d.digest, now
			d.manifestProbe, d.manifest = probe, parsed
			return nil
		})
	}
	_ = eg.Wait()
}

// probeDue — пора ли искать манифест заново.
func (s *Service) probeDue(prev *workloadModel.Manifest, digest string, now time.Time) bool {
	switch {
	case prev == nil:
		return true
	case digest != "" && digest != prev.Digest:
		return true
	case prev.Status == workloadModel.ManifestOk || prev.Status == workloadModel.ManifestPartial:
		return now.Sub(prev.CheckedAt) >= s.conf.Manifest.RefreshAfter
	default:
		return now.Sub(prev.CheckedAt) >= s.conf.Manifest.RetryAfter
	}
}

// probe стучится в порты-кандидаты пода, пока не найдёт манифест.
func (s *Service) probe(ctx context.Context, pod k8sModel.Pod, scrapePorts map[string][]int, deployed bool) (*workloadModel.Manifest, *localModel.ParsedManifest) {
	path := s.conf.Manifest.Path
	if p := strings.TrimSpace(pod.Annotations[s.conf.Manifest.AnnotationPrefix+"path"]); strings.HasPrefix(p, "/") {
		path = p
	}

	known, rest := s.candidatePorts(pod, scrapePorts[pod.Namespace+"/"+pod.Name])
	candidates := known
	if deployed {
		candidates = append(candidates, rest...)
	}

	result := &workloadModel.Manifest{Status: workloadModel.ManifestUnreachable}
	headers := map[string]string{"User-Agent": constant.ServiceName + "/" + constant.Version, "X-Pulse-Request-Id": "indexer"}

	for i, port := range candidates {
		timeout := lo.Ternary(i < len(known), probeTimeout, fallbackProbeTimeout)
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		resp, err := s.pods.GetPod(probeCtx, svcproxyModel.PodTarget{Namespace: pod.Namespace, Pod: pod.Name, IP: pod.IP, Port: port},
			path, nil, headers, localModel.ManifestMaxBytes)
		cancel()

		switch {
		case err != nil:
			slog.Debug("indexer: manifest probe", "pod", pod.Namespace+"/"+pod.Name, "port", port, "error", err)
			result.Tried = append(result.Tried, fmt.Sprintf("%d: нет ответа", port))
			continue
		case resp.StatusCode != 200:
			result.Tried = append(result.Tried, fmt.Sprintf("%d: %d", port, resp.StatusCode))
			result.Status = workloadModel.ManifestAbsent // HTTP-сервер есть, манифеста нет
			continue
		case !resp.Truncated && !localModel.IsManifest(resp.Body):
			// 200 на любой путь (SPA, catch-all) — манифеста здесь нет
			result.Tried = append(result.Tried, fmt.Sprintf("%d: 200, не манифест", port))
			result.Status = workloadModel.ManifestAbsent
			continue
		}

		result.Tried = append(result.Tried, fmt.Sprintf("%d: манифест", port))
		result.Port = port
		if resp.Truncated {
			result.Status, result.Reasons = workloadModel.ManifestInvalid, []string{fmt.Sprintf("манифест больше %d KB", localModel.ManifestMaxBytes>>10)}
			return result, nil
		}
		parsed, err := localModel.ParseManifest(resp.Body)
		if err != nil {
			result.Status, result.Reasons = workloadModel.ManifestInvalid, []string{err.Error()}
			return result, nil
		}
		result.Raw = resp.Body
		result.Status, result.Reasons = workloadModel.ManifestOk, parsed.Problems
		if len(parsed.Problems) > 0 {
			result.Status = workloadModel.ManifestPartial
		}
		return result, parsed
	}

	if len(candidates) == 0 {
		result.Reasons = []string{"у пода нет портов-кандидатов: задайте аннотацию " + s.conf.Manifest.AnnotationPrefix + "port"}
	}
	return result, nil
}

// candidatePorts — порты по порядку стандарта: аннотация, порт /metrics (цели Prometheus),
// порты с именами system и http*, порты по умолчанию; rest — остальные TCP-порты пода, кроме
// заведомо не-HTTP (перебираются только после выкатки).
func (s *Service) candidatePorts(pod k8sModel.Pod, scrape []int) (known, rest []int) {
	add := func(list *[]int, port int) {
		if port > 0 && port < 65536 && !slices.Contains(known, port) && !slices.Contains(rest, port) {
			*list = append(*list, port)
		}
	}

	if port, err := strconv.Atoi(strings.TrimSpace(pod.Annotations[s.conf.Manifest.AnnotationPrefix+"port"])); err == nil {
		add(&known, port)
	}
	for _, port := range scrape {
		add(&known, port)
	}
	tcp := lo.Filter(pod.Ports, func(p k8sModel.PodPort, _ int) bool { return p.Protocol == "" || p.Protocol == "TCP" })
	for _, p := range tcp {
		if p.Name == "system" {
			add(&known, int(p.Port))
		}
	}
	for _, p := range tcp {
		if p.Name == "http" || strings.HasPrefix(p.Name, "http-") {
			add(&known, int(p.Port))
		}
	}
	for _, port := range s.conf.Manifest.DefaultPorts {
		add(&known, port)
	}
	for _, p := range tcp {
		if !slices.Contains(s.conf.Manifest.SkipPorts, int(p.Port)) {
			add(&rest, int(p.Port))
		}
	}
	return known, rest
}

// scrapePorts — порты, с которых Prometheus собирает /metrics, по подам («ns/pod»): манифест
// живёт на том же служебном порту. Prometheus недоступен — без этого кандидата.
func (s *Service) scrapePorts(ctx context.Context) map[string][]int {
	if s.prom == nil {
		return nil
	}
	samples, err := s.prom.Query(ctx, `up{pod!=""}`, time.Time{})
	if err != nil {
		slog.Warn("indexer: prometheus targets are unavailable for manifest discovery", "error", err)
		return nil
	}
	result := make(map[string][]int, len(samples))
	for _, sample := range samples {
		_, portStr, err := net.SplitHostPort(sample.Labels["instance"])
		if err != nil {
			continue
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			continue
		}
		key := sample.Labels["namespace"] + "/" + sample.Labels["pod"]
		if !slices.Contains(result[key], port) {
			result[key] = append(result[key], port)
		}
	}
	for key := range result {
		sort.Ints(result[key])
	}
	return result
}

// readyPods — готовые поды workload'а с адресом, по имени (детерминированно).
func readyPods(pods []k8sModel.Pod, d *workloadDraft) []k8sModel.Pod {
	if d.Selector == "" {
		return nil
	}
	selector, err := labels.Parse(d.Selector)
	if err != nil {
		return nil
	}
	result := lo.Filter(pods, func(p k8sModel.Pod, _ int) bool {
		return p.Namespace == d.Namespace && p.Ready && p.IP != "" && selector.Matches(labels.Set(p.Labels))
	})
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

// parseStored — прошлый принятый манифест из каталога; разобрать не вышло — как будто его нет.
func parseStored(m *workloadModel.Manifest) *localModel.ParsedManifest {
	if len(m.Raw) == 0 || (m.Status != workloadModel.ManifestOk && m.Status != workloadModel.ManifestPartial) {
		return nil
	}
	parsed, err := localModel.ParseManifest(m.Raw)
	if err != nil {
		slog.Warn("indexer: stored manifest no longer passes validation", "error", err)
		return nil
	}
	return parsed
}

// applyManifest — правка каталога из манифеста: всё, что сервис рассказал о себе. Ручки
// помечаются workload'ом, на поды которого их вызывать.
func applyManifest(edit *svcModel.Edit, m *localModel.ParsedManifest, d *workloadDraft) {
	metadata := m.Metadata
	ref := &svcModel.WorkloadRef{Namespace: d.Namespace, Kind: d.Kind, Name: d.Name}
	metadata.Endpoints = lo.Map(metadata.Endpoints, func(e svcModel.Endpoint, _ int) svcModel.Endpoint {
		e.Workload = ref
		return e
	})

	edit.Name = new(m.Name)
	edit.Title = new(m.Title)
	edit.Description = new(m.Description)
	edit.Criticality = new(m.Criticality)
	edit.OwnerTeam = new(m.OwnerTeam)
	edit.OwnerContacts = new(lo.CoalesceSliceOrEmpty(m.OwnerContacts))
	edit.Aliases = new(lo.CoalesceSliceOrEmpty(m.Aliases))
	edit.MetadataPresent = new(true)
	edit.Metadata = &metadata
	if lo.FromPtr(edit.RepoUrl) == "" && m.RepoUrl != "" {
		edit.RepoUrl = new(m.RepoUrl)
	}
}

// mergeManifest добавляет к правке сервиса ручки и зависимости манифеста ещё одного его
// workload'а (loom/server + loom/artifact); повторы id — пропускаются.
func mergeManifest(edit *svcModel.Edit, m *localModel.ParsedManifest, d *workloadDraft) error {
	other := &svcModel.Edit{}
	applyManifest(other, m, d)

	var errs []error
	for _, e := range other.Metadata.Endpoints {
		if slices.ContainsFunc(edit.Metadata.Endpoints, func(x svcModel.Endpoint) bool { return x.Id == e.Id }) {
			errs = append(errs, fmt.Errorf("ручка %s объявлена в нескольких workload'ах сервиса", e.Id))
			continue
		}
		edit.Metadata.Endpoints = append(edit.Metadata.Endpoints, e)
	}
	for _, dep := range other.Metadata.Dependencies {
		if !slices.ContainsFunc(edit.Metadata.Dependencies, func(x svcModel.Dependency) bool { return x.Id == dep.Id }) {
			edit.Metadata.Dependencies = append(edit.Metadata.Dependencies, dep)
		}
	}
	return errors.Join(errs...)
}

// resolveManifestCommits — коммит сборки из манифеста: сервис знает его точно, угадывать по
// реестру и запускам CI не нужно.
func resolveManifestCommits(drafts []*workloadDraft) {
	for _, d := range drafts {
		if d.commit == "" && d.manifest != nil && d.manifest.Commit != "" {
			d.commit = d.manifest.Commit
		}
	}
}
