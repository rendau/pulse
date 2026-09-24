package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/labels"

	deployModel "github.com/mechta-market/pulse/internal/domain/deploy/model"
	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	githubModel "github.com/mechta-market/pulse/internal/service/github/model"
	localConstant "github.com/mechta-market/pulse/internal/service/indexer/service/constant"
	localModel "github.com/mechta-market/pulse/internal/service/indexer/service/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
	"github.com/mechta-market/pulse/internal/util/imageref"
)

// Run выполняет один цикл: кластер → образы → репозитории → service.yaml → каталог.
// Недоступность GitHub не останавливает цикл: топология обновляется, метаданные
// остаются от предыдущего цикла (поля Edit остаются nil и колонки не трогаются).
func (s *Service) Run(ctx context.Context) error {
	started := time.Now()
	now := started.UTC()
	stats := indexerStats{}

	workloads, err := s.k8s.ListWorkloads(ctx)
	if err != nil {
		return fmt.Errorf("k8s.ListWorkloads: %w", err)
	}
	stats.Workloads = len(workloads)

	// все поды кластера одним запросом: они нужны для digest'ов запущенных образов
	pods, err := s.k8s.ListPods(ctx, "", "")
	if err != nil {
		slog.Warn("indexer: pods are unavailable, digests will not be updated", "error", err)
		pods = nil
	}

	// 1. кластер → черновики
	drafts := lo.Map(workloads, func(w k8sModel.Workload, _ int) *workloadDraft {
		return s.newDraft(w, pods)
	})

	// 1.1 образы, которые запускаются только Job'ами оркестратора: свой сервис и workload
	drafts = append(drafts, s.jobDrafts(ctx, drafts, pods)...)

	// 1.2 репозиторий из привязки пакета ghcr: имя образа может не совпадать с репой
	s.resolvePackageRepos(ctx, drafts)

	// 2. репозитории → service.yaml (параллельно, с общим кэшем на цикл)
	repos := lo.Uniq(lo.FilterMap(drafts, func(d *workloadDraft, _ int) (string, bool) {
		return d.repoUrl, d.repoUrl != ""
	}))
	metadata := s.fetchMetadata(ctx, repos, &stats)

	// 3. digest → коммит (параллельно, кэш живёт в registry-клиенте)
	s.resolveCommits(ctx, drafts, &stats)

	// 4. история деплоев: смена образа/digest относительно прошлого цикла
	stats.Deploys = s.recordDeploys(ctx, drafts)

	// 5. группировка по сервисам и запись каталога
	services := s.buildServices(ctx, drafts, metadata, now)
	stats.Services = len(services)

	// k8s Services и маршруты ruto — для графа связей и имён сервисов в кластере
	topo := s.loadTopology(ctx, drafts, now)
	if topo.complete {
		for _, edit := range services {
			edit.ClusterNames = new(lo.CoalesceSliceOrEmpty(topo.clusterNames(*edit.Name, drafts)))
		}
	}

	for _, edit := range services {
		if err = s.svc.UpdateOrCreate(ctx, edit); err != nil {
			return fmt.Errorf("svc.UpdateOrCreate(%s): %w", lo.FromPtr(edit.Name), err)
		}
	}

	workloadEdits := lo.Map(drafts, func(d *workloadDraft, _ int) *workloadModel.Edit {
		return d.toEdit(s.conf.Cluster, now)
	})
	if err = s.workload.UpdateOrCreateMany(ctx, workloadEdits); err != nil {
		return fmt.Errorf("workload.UpdateOrCreateMany: %w", err)
	}

	// 6. связи между сервисами из env/configmap подов (фаза 5)
	stats.Dependencies = s.recordDependencies(ctx, drafts, topo, now)

	// 7. чистка: что не видели дольше stale_after, того больше нет
	staleBefore := now.Add(-s.conf.StaleAfter)
	if stats.StaleWorkloads, err = s.workload.DeleteStale(ctx, s.conf.Cluster, staleBefore); err != nil {
		return fmt.Errorf("workload.DeleteStale: %w", err)
	}
	staleServices, err := s.svc.DeleteStale(ctx, staleBefore)
	if err != nil {
		return fmt.Errorf("svc.DeleteStale: %w", err)
	}
	if _, err = s.depend.DeleteStale(ctx, s.conf.Cluster, staleBefore); err != nil {
		return fmt.Errorf("depend.DeleteStale: %w", err)
	}
	stats.StaleServices = len(staleServices)

	stats.Duration = time.Since(started)
	slog.Info("indexer cycle done",
		"workloads", stats.Workloads,
		"services", stats.Services,
		"with_metadata", stats.WithMetadata,
		"metadata_errors", stats.MetadataErrors,
		"commits_resolved", stats.CommitsResolved,
		"deploys", stats.Deploys,
		"dependencies", stats.Dependencies,
		"stale_workloads", stats.StaleWorkloads,
		"stale_services", stats.StaleServices,
		"github_unavailable", stats.GithubUnavailable,
		"duration", stats.Duration.String(),
	)

	return nil
}

type indexerStats struct {
	Workloads         int
	Services          int
	WithMetadata      int
	MetadataErrors    int
	CommitsResolved   int
	Deploys           int
	Dependencies      int
	StaleWorkloads    int64
	StaleServices     int
	Duration          time.Duration
	GithubUnavailable bool
}

// workloadDraft — workload кластера с результатами сопоставления.
type workloadDraft struct {
	k8sModel.Workload

	image    imageref.Ref
	imageRaw string
	repoUrl  string
	// serviceKey — имя сервиса по умолчанию (имя образа); может быть заменено name из service.yaml
	serviceKey string
	digest     string
	commit     string
}

// newDraft выбирает главный контейнер (первый с образом из известного registry, иначе
// первый) и собирает digest из статусов подов.
func (s *Service) newDraft(w k8sModel.Workload, pods []k8sModel.Pod) *workloadDraft {
	draft := &workloadDraft{Workload: w}

	var mainContainer k8sModel.Container
	for i, c := range w.Containers {
		ref, err := imageref.Parse(c.Image)
		if err != nil {
			continue
		}
		repoUrl, mapped := s.mapper.RepoUrl(ref)
		if i == 0 || mapped {
			mainContainer = c
			draft.image, draft.imageRaw, draft.repoUrl = ref, c.Image, repoUrl
		}
		if mapped {
			break
		}
	}

	if draft.repoUrl != "" {
		// имя репозитория, а не образа: одна репа может собирать несколько образов
		// (rendau/loom/server и rendau/loom/artifact — один сервис loom)
		draft.serviceKey = draft.image.RepoName()
	} else {
		// сторонний образ (postgres, redis): сервис называется по workload'у
		draft.serviceKey = w.Name
	}

	if imageref.LooksLikeCommit(draft.image.Tag) {
		draft.commit = draft.image.Tag
	}

	// digest — из любого пода workload'а, у которого главный контейнер уже запущен
	if w.Selector != "" {
		if selector, err := labels.Parse(w.Selector); err == nil {
			draft.digest = findDigest(pods, w.Namespace, selector, mainContainer.Name)
		}
	}

	return draft
}

func findDigest(pods []k8sModel.Pod, namespace string, selector labels.Selector, containerName string) string {
	for _, pod := range pods {
		if pod.Namespace != namespace || !selector.Matches(labels.Set(pod.Labels)) {
			continue
		}
		for _, c := range pod.Containers {
			if c.Name != containerName {
				continue
			}
			if digest := imageref.DigestFromImageID(c.ImageID); digest != "" {
				return digest
			}
		}
	}
	return ""
}

func (d *workloadDraft) toEdit(cluster string, now time.Time) *workloadModel.Edit {
	edit := &workloadModel.Edit{
		Cluster:         new(cluster),
		Namespace:       new(d.Namespace),
		Kind:            new(d.Kind),
		Name:            new(d.Name),
		ServiceName:     new(d.serviceKey),
		ReplicasDesired: new(d.ReplicasDesired),
		Image:           new(d.imageRaw),
		Selector:        new(d.Selector),
		ConfigRefs:      new(lo.CoalesceSliceOrEmpty(d.ConfigRefs)),
		FirstSeen:       new(now),
		LastSeen:        new(now),
	}
	// digest и коммит обновляем только когда узнали их: nil оставляет прошлое значение
	if d.digest != "" {
		edit.ImageDigest = new(d.digest)
	}
	if d.commit != "" {
		edit.DeployedCommit = new(d.commit)
	}
	return edit
}

// metadataResult — результат чтения service.yaml для репозитория.
type metadataResult struct {
	yaml *localModel.ServiceYaml
	// found=false при отсутствии файла; err — GitHub недоступен или файл невалиден
	found bool
	err   error
	// repoDescription — описание и topics репозитория: описание сервиса, если его нет в
	// service.yaml (или нет самого файла) — поиску есть по чему искать, кроме имени
	repoDescription string
}

func (s *Service) fetchMetadata(ctx context.Context, repos []string, stats *indexerStats) map[string]metadataResult {
	results := make(map[string]metadataResult, len(repos))
	var mu sync.Mutex

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(localConstant.GithubConcurrency)

	for _, repoUrl := range repos {
		eg.Go(func() error {
			result := metadataResult{}

			raw, found, err := s.github.GetFileContent(egCtx, repoUrl, localConstant.ServiceYamlPath)
			switch {
			case err != nil:
				result.err = fmt.Errorf("github: %w", err)
			case found:
				result.found = true
				result.yaml, result.err = localModel.ParseServiceYaml(raw)
				if result.err != nil {
					slog.Warn("indexer: invalid service.yaml", "repo", repoUrl, "error", result.err)
				}
			}

			// описание — уточнение: GitHub его не отдал — сервис всё равно в каталоге
			if result.err == nil {
				if info, err := s.github.RepoInfo(egCtx, repoUrl); err != nil {
					slog.Debug("indexer: repo info", "repo", repoUrl, "error", err)
				} else {
					result.repoDescription = repoDescription(info)
				}
			}

			mu.Lock()
			results[repoUrl] = result
			mu.Unlock()
			return nil
		})
	}
	_ = eg.Wait()

	for _, r := range results {
		switch {
		case r.err != nil:
			stats.MetadataErrors++
		case r.found:
			stats.WithMetadata++
		}
	}
	// если ни один репозиторий не прочитался — GitHub, скорее всего, недоступен целиком
	stats.GithubUnavailable = len(results) > 0 && stats.MetadataErrors == len(results)

	return results
}

// resolveCommits — коммит запущенного образа: OCI-label revision, а если его нет (сервисы
// собираются без label'ов) — по сборке в GitHub Actions, опубликовавшей этот digest в ghcr.
func (s *Service) resolveCommits(ctx context.Context, drafts []*workloadDraft, stats *indexerStats) {
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(localConstant.RegistryConcurrency)

	for _, d := range drafts {
		if d.commit != "" || d.digest == "" || d.repoUrl == "" {
			continue
		}
		eg.Go(func() error {
			labelsMap, err := s.registry.GetImageLabels(egCtx, d.image.WithDigest(d.digest).String())
			if err != nil {
				slog.Debug("indexer: image labels are unavailable", "image", d.imageRaw, "error", err)
			}
			if d.commit = labelsMap[localConstant.RevisionLabel]; d.commit != "" || d.image.Host != localConstant.GhcrHost {
				return nil
			}

			if d.commit, err = s.github.ResolveImageCommit(egCtx, d.repoUrl, d.image.Path, d.digest); err != nil {
				slog.Warn("indexer: image build commit is unavailable", "image", d.imageRaw, "error", err)
			}
			return nil
		})
	}
	_ = eg.Wait()

	stats.CommitsResolved = lo.CountBy(drafts, func(d *workloadDraft) bool { return d.commit != "" })
}

// resolvePackageRepos заменяет repo_url, выведенный из пути образа по шаблону, на
// репозиторий, к которому GitHub привязал пакет ghcr (dpm → dp-mechta, ruto-core → ruto).
// Имя сервиса не меняется. Пакет без привязки или ошибка GitHub — остаётся шаблонный URL.
func (s *Service) resolvePackageRepos(ctx context.Context, drafts []*workloadDraft) {
	ghcr := lo.Filter(drafts, func(d *workloadDraft, _ int) bool {
		return d.repoUrl != "" && d.image.Host == localConstant.GhcrHost
	})
	paths := lo.Uniq(lo.Map(ghcr, func(d *workloadDraft, _ int) string { return d.image.Path }))

	var mu sync.Mutex
	repos := make(map[string]string, len(paths))
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(localConstant.RegistryConcurrency)
	for _, imagePath := range paths {
		eg.Go(func() error {
			repoUrl, err := s.github.PackageRepoUrl(egCtx, imagePath)
			if err != nil {
				slog.Warn("indexer: package repository is unavailable", "package", imagePath, "error", err)
				return nil
			}
			if repoUrl != "" {
				mu.Lock()
				repos[imagePath] = repoUrl
				mu.Unlock()
			}
			return nil
		})
	}
	_ = eg.Wait()

	for _, d := range ghcr {
		if repoUrl, ok := repos[d.image.Path]; ok {
			d.repoUrl = repoUrl
		}
	}
}

// buildServices группирует черновики по сервисам и собирает правки каталога.
// Имя сервиса: name из service.yaml → имя из каталога (по repo_url, если GitHub недоступен)
// → имя образа / workload'а.
func (s *Service) buildServices(ctx context.Context, drafts []*workloadDraft, metadata map[string]metadataResult, now time.Time) []*svcModel.Edit {
	// прошлые имена по repo_url — чтобы при недоступном GitHub не потерять переименование
	previousNames := s.previousNamesByRepo(ctx)

	edits := make(map[string]*svcModel.Edit, len(drafts))

	for _, d := range drafts {
		edit := &svcModel.Edit{
			Name:      new(d.serviceKey),
			RepoUrl:   new(d.repoUrl),
			FirstSeen: new(now),
			LastSeen:  new(now),
		}

		if d.repoUrl != "" {
			meta := metadata[d.repoUrl]
			switch {
			case meta.err == nil && meta.found:
				edit = localModel.DecodeServiceYaml(meta.yaml)
				edit.RepoUrl, edit.FirstSeen, edit.LastSeen = new(d.repoUrl), new(now), new(now)
				if lo.FromPtr(edit.Description) == "" {
					edit.Description = new(meta.repoDescription)
				}
			case meta.err == nil && !meta.found:
				// файла нет — каталог из кластера, метаданные сбрасываются; описание — из репозитория
				edit.MetadataPresent = new(false)
				edit.Title, edit.Description, edit.Criticality, edit.OwnerTeam = new(""), new(meta.repoDescription), new(""), new("")
				edit.Aliases, edit.OwnerContacts = new([]string{}), new([]string{})
				edit.Metadata = new(svcModel.Metadata{})
			default:
				// GitHub недоступен: имя из прошлого цикла, метаданные не трогаем
				if prev, ok := previousNames[d.repoUrl]; ok {
					edit.Name = new(prev)
				}
			}
			d.serviceKey = *edit.Name
		} else {
			// сторонний образ: метаданных нет и не будет
			edit.MetadataPresent = new(false)
		}

		if existing, ok := edits[*edit.Name]; ok {
			// несколько workloads одного сервиса: правка одна, last_seen общий
			existing.LastSeen = edit.LastSeen
			continue
		}
		edits[*edit.Name] = edit
	}

	return lo.Values(edits)
}

func (s *Service) previousNamesByRepo(ctx context.Context) map[string]string {
	services, _, err := s.svc.List(ctx, &svcModel.ListReq{})
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			slog.Warn("indexer: previous catalog is unavailable", "error", err)
		}
		return nil
	}

	return lo.SliceToMap(
		lo.Filter(services, func(v *svcModel.Main, _ int) bool { return v.RepoUrl != "" }),
		func(v *svcModel.Main) (string, string) { return v.RepoUrl, v.Name },
	)
}

// recordDeploys сравнивает черновики с прошлым состоянием каталога и пишет факт деплоя,
// когда у workload'а сменился образ или digest запущенного образа. Первое появление
// workload'а деплоем не считается.
func (s *Service) recordDeploys(ctx context.Context, drafts []*workloadDraft) int {
	previous, _, err := s.workload.List(ctx, &workloadModel.ListReq{Cluster: new(s.conf.Cluster)})
	if err != nil {
		slog.Warn("indexer: previous workloads are unavailable, deploys will not be recorded", "error", err)
		return 0
	}
	byKey := lo.SliceToMap(previous, func(w *workloadModel.Main) (workloadModel.Key, *workloadModel.Main) { return w.Key(), w })

	count := 0
	for _, d := range drafts {
		prev, ok := byKey[workloadModel.Key{Cluster: s.conf.Cluster, Namespace: d.Namespace, Kind: d.Kind, Name: d.Name}]
		if !ok {
			continue
		}
		digestChanged := d.digest != "" && prev.ImageDigest != "" && d.digest != prev.ImageDigest
		imageChanged := d.imageRaw != "" && prev.Image != "" && d.imageRaw != prev.Image
		if !digestChanged && !imageChanged {
			continue
		}

		_, err = s.deploy.Create(ctx, &deployModel.Edit{
			Cluster: new(s.conf.Cluster), Namespace: new(d.Namespace), Kind: new(d.Kind), Name: new(d.Name),
			ServiceName:     new(prev.ServiceName),
			Image:           new(d.imageRaw),
			ImageDigest:     new(d.digest),
			DeployedCommit:  new(d.commit),
			PrevImage:       new(prev.Image),
			PrevImageDigest: new(prev.ImageDigest),
			PrevCommit:      new(prev.DeployedCommit),
		})
		if err != nil {
			slog.Warn("indexer: deploy record failed", "workload", d.Namespace+"/"+d.Name, "error", err)
			continue
		}
		count++
		slog.Info("indexer: deploy detected", "workload", d.Kind+"/"+d.Namespace+"/"+d.Name,
			"image", d.imageRaw, "digest", d.digest, "commit", d.commit, "prev_digest", prev.ImageDigest, "prev_commit", prev.DeployedCommit)
	}

	return count
}

// repoDescription — описание репозитория и его topics одной строкой («Платёжный шлюз.
// Topics: payments, acquiring»).
func repoDescription(info *githubModel.Repo) string {
	description := strings.TrimSpace(info.Description)
	if len(info.Topics) == 0 {
		return description
	}
	topics := "Topics: " + strings.Join(info.Topics, ", ")
	if description == "" {
		return topics
	}
	return strings.TrimSuffix(description, ".") + ". " + topics
}
