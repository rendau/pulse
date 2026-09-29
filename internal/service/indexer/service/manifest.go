package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/rendau/pulse/internal/constant"
	svcModel "github.com/rendau/pulse/internal/domain/svc/model"
	workloadModel "github.com/rendau/pulse/internal/domain/workload/model"
	localModel "github.com/rendau/pulse/internal/service/indexer/service/model"
	k8sModel "github.com/rendau/pulse/internal/service/k8s/model"
	svcproxyModel "github.com/rendau/pulse/internal/service/svcproxy/model"
	"github.com/rendau/pulse/internal/util/imageref"
	"github.com/rendau/pulse/internal/util/neterr"
)

// Поиск манифеста сервиса через его k8s Service (docs/service-manifest.md, «Как pulse находит
// манифест»): порт Service с именем manifest.service_port (system), отвечает любой под за Service.

const (
	manifestConcurrency = 8
	// таймаут запроса манифеста: по стандарту манифест отдаётся за ≤ 2 с
	probeTimeout = 2 * time.Second
)

// probeManifests ищет манифест у workload'ов, которым пора: сменился образ или Service
// workload'а, принятый манифест старше RefreshAfter, неудача старше RetryAfter. Остальным —
// прошлый результат из каталога. Итог — в draft.manifest (действующий манифест) и
// draft.manifestProbe (новый результат поиска).
func (s *Service) probeManifests(ctx context.Context, drafts []*workloadDraft, pods []k8sModel.Pod, previous map[workloadModel.Key]*workloadModel.Main, now time.Time) {
	if s.caller == nil || s.k8s == nil || s.conf.Manifest.Path == "" || previous == nil {
		return
	}

	// Service — единственный путь к манифесту: без их списка остаются прошлые результаты
	services, servicesErr := s.k8s.ListServices(ctx, "")
	if servicesErr != nil {
		slog.Warn("indexer: k8s services are unavailable, manifests will not be updated", "error", servicesErr)
	}

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(manifestConcurrency)
	for _, d := range drafts {
		prev := previous[d.key(s.conf.Cluster)]
		var prevManifest *workloadModel.Manifest
		if prev != nil && prev.Manifest.Status != "" {
			prevManifest = &prev.Manifest
		}

		ready := readyPods(pods, d)
		target, found := s.manifestTarget(ready, services)
		// выкатка: пока за Service есть поды прошлой сборки, ответить могут они — с её манифестом
		// и коммитом; подов нет — Service ответить некому
		if servicesErr != nil || len(ready) == 0 || d.rolling || !settled(ready, d) || !s.probeDue(prevManifest, d.digest, target, now) {
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

		if !found {
			d.manifestProbe = &workloadModel.Manifest{Status: workloadModel.ManifestAbsent, Digest: d.digest, CheckedAt: now,
				Reasons: []string{fmt.Sprintf("нет k8s Service с портом %s, который ведёт на поды workload'а", s.conf.Manifest.ServicePort)}}
			d.manifest = nil
			continue
		}
		eg.Go(func() error {
			probe, parsed := s.probe(egCtx, target)
			probe.Digest, probe.CheckedAt = d.digest, now
			d.manifestProbe, d.manifest = probe, parsed
			return nil
		})
	}
	_ = eg.Wait()
}

// probeDue — пора ли искать манифест заново.
func (s *Service) probeDue(prev *workloadModel.Manifest, digest string, target svcproxyModel.ServiceTarget, now time.Time) bool {
	switch {
	case prev == nil:
		return true
	case digest != "" && digest != prev.Digest:
		return true
	case prev.Service != target.Service || prev.Port != target.Port:
		// Service workload'а появился, пропал или сменил порт
		return true
	case prev.Status == workloadModel.ManifestOk || prev.Status == workloadModel.ManifestPartial:
		return now.Sub(prev.CheckedAt) >= s.conf.Manifest.RefreshAfter
	default:
		return now.Sub(prev.CheckedAt) >= s.conf.Manifest.RetryAfter
	}
}

// manifestTarget — k8s Service, который ведёт на поды workload'а (селектор совпал с лейблами
// пода), с портом manifest.service_port: через него pulse вызывает манифест, ручку состояния и
// диагностические ручки. Таких несколько — первый по имени.
func (s *Service) manifestTarget(ready []k8sModel.Pod, services []k8sModel.Service) (svcproxyModel.ServiceTarget, bool) {
	if len(ready) == 0 {
		return svcproxyModel.ServiceTarget{}, false
	}
	pod := ready[0]
	isManifestPort := func(p k8sModel.ServicePort) bool { return p.Name == s.conf.Manifest.ServicePort }
	matched := lo.Filter(services, func(svc k8sModel.Service, _ int) bool {
		return svc.Namespace == pod.Namespace && len(svc.Selector) > 0 && slices.ContainsFunc(svc.Ports, isManifestPort) &&
			labels.SelectorFromSet(svc.Selector).Matches(labels.Set(pod.Labels))
	})
	if len(matched) == 0 {
		return svcproxyModel.ServiceTarget{}, false
	}
	svc := lo.MinBy(matched, func(a, b k8sModel.Service) bool { return a.Name < b.Name })
	port, _ := lo.Find(svc.Ports, isManifestPort)
	return svcproxyModel.ServiceTarget{Namespace: svc.Namespace, Service: svc.Name, Port: int(port.Port)}, true
}

// probe запрашивает манифест через Service workload'а.
func (s *Service) probe(ctx context.Context, target svcproxyModel.ServiceTarget) (*workloadModel.Manifest, *localModel.ParsedManifest) {
	where := fmt.Sprintf("%s:%d", target.Service, target.Port)
	result := &workloadModel.Manifest{Status: workloadModel.ManifestUnreachable, Service: target.Service, Port: target.Port}

	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	resp, err := s.caller.GetService(ctx, target, s.conf.Manifest.Path, nil, map[string]string{
		"User-Agent":         constant.ServiceName + "/" + constant.Version,
		"X-Pulse-Request-Id": "indexer",
	}, localModel.ManifestMaxBytes)

	switch {
	case err != nil:
		slog.Debug("indexer: manifest probe", "service", target.Namespace+"/"+where, "error", err)
		result.Tried = []string{where + ": " + noAnswer(err)}
		return result, nil
	case resp.StatusCode != 200:
		// HTTP-сервер есть, манифеста нет
		result.Status, result.Tried = workloadModel.ManifestAbsent, []string{fmt.Sprintf("%s: %d", where, resp.StatusCode)}
		return result, nil
	case resp.Truncated && !localModel.LooksLikeManifest(resp.Body), !resp.Truncated && !localModel.IsManifest(resp.Body):
		// 200 на любой путь (SPA, catch-all) — манифеста здесь нет
		result.Status, result.Tried = workloadModel.ManifestAbsent, []string{where + ": 200, не манифест"}
		return result, nil
	}

	result.Tried = []string{where + ": манифест"}
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

// noAnswer — «нет ответа» с видом ошибки: по нему видно, где искать причину (таймаут — сеть
// или сервис висит, отклонено — порт не слушается, DNS — нет такого Service).
func noAnswer(err error) string {
	if reason := neterr.Reason(err); reason != "" {
		return "нет ответа (" + reason + ")"
	}
	return "нет ответа"
}

// readyPods — готовые поды workload'а с адресом, по имени (детерминированно): по ним находится
// Service workload'а и видно, закончилась ли выкатка.
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

// settled — выкатка закончилась: все готовые поды workload'а на образе d.digest. Иначе за Service
// отвечали бы и поды прошлой сборки — с её манифестом и коммитом.
func settled(ready []k8sModel.Pod, d *workloadDraft) bool {
	return d.digest == "" || lo.EveryBy(ready, func(p k8sModel.Pod) bool {
		return lo.ContainsBy(p.Containers, func(c k8sModel.PodContainer) bool {
			return c.Name == d.container && imageref.DigestFromImageID(c.ImageID) == d.digest
		})
	})
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
	// бизнес-смысл у сервиса один: берётся первый описанный
	if edit.Metadata.Domain == nil {
		edit.Metadata.Domain = other.Metadata.Domain
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
