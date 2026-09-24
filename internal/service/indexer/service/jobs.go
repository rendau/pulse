package service

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	"github.com/samber/lo"

	"github.com/mechta-market/pulse/internal/constant"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
	"github.com/mechta-market/pulse/internal/util/imageref"
	"github.com/mechta-market/pulse/internal/util/jobprefix"
)

// maxJobDrafts — потолок workload'ов из Job'ов оркестраторов за цикл.
const maxJobDrafts = 50

// jobDrafts — сервисы для образов, которые запускаются только Job'ами оркестратора (Argo
// Workflows, Airflow и т.п.): код задач лежит в своём репозитории и собирается своим образом,
// но своего Deployment/CronJob у него нет — без этого ошибки задач приписывались бы
// оркестратору, а коммиты и деплои кода задач были бы не видны.
//
// Берутся Job'ы без владельца (не CronJob) с образом из известного registry, чей сервис не
// запущен ни одним workload'ом. Workload — семейство Job'ов одного образа в namespace'е: имя —
// общий префикс имён Job'ов (jobprefix), по нему находятся поды — и живые, и удалённые в Loki.
// Ошибка чтения Job'ов — не ошибка цикла: остальной каталог обновляется.
func (s *Service) jobDrafts(ctx context.Context, drafts []*workloadDraft, pods []k8sModel.Pod) []*workloadDraft {
	jobs, err := s.k8s.ListJobs(ctx, "")
	if err != nil {
		slog.Warn("indexer: jobs are unavailable, orchestrated images are not indexed", "error", err)
		return nil
	}

	known := lo.SliceToMap(drafts, func(d *workloadDraft) (string, struct{}) { return d.serviceKey, struct{}{} })

	type family struct {
		namespace string
		draft     *workloadDraft // по последнему Job'у: образ, контейнеры, config refs
		jobs      []string
	}
	families := map[string]*family{}

	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
	for _, job := range jobs {
		if job.OwnerKind != "" {
			continue // CronJob — уже workload каталога
		}
		draft := s.newDraft(k8sModel.Workload{
			Kind: constant.WorkloadKindJob, Namespace: job.Namespace, Name: job.Name,
			Containers: job.Containers, ConfigRefs: job.ConfigRefs, CreatedAt: job.CreatedAt,
		}, nil)
		if draft.repoUrl == "" {
			continue // сторонний образ (busybox, миграции из чужих образов)
		}
		if _, ok := known[draft.serviceKey]; ok {
			continue // у сервиса есть свой workload (Helm-хуки, миграции)
		}

		key := job.Namespace + "/" + draft.serviceKey
		f, ok := families[key]
		if !ok {
			f = &family{namespace: job.Namespace, draft: draft}
			families[key] = f
		}
		f.jobs = append(f.jobs, job.Name)
	}

	result := make([]*workloadDraft, 0, len(families))
	for _, key := range lo.Keys(families) {
		f := families[key]
		jobSet := lo.SliceToMap(f.jobs, func(j string) (string, struct{}) { return j, struct{}{} })
		foreign := lo.FilterMap(pods, func(p k8sModel.Pod, _ int) (string, bool) {
			_, own := jobSet[jobprefix.JobName(p.Labels)]
			return p.Name, p.Namespace == f.namespace && !own
		})

		for _, prefix := range jobprefix.Prefixes(f.jobs, foreign) {
			draft := *f.draft
			draft.Name = strings.TrimSuffix(prefix, "-")
			draft.digest = jobDigest(pods, f.namespace, prefix, jobSet, draft.imageRaw, draft.Containers)
			result = append(result, &draft)
		}
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Namespace+result[i].Name < result[j].Namespace+result[j].Name })
	if len(result) > maxJobDrafts {
		result = result[:maxJobDrafts]
	}
	return result
}

// jobDigest — digest главного контейнера из самого свежего пода семейства.
func jobDigest(pods []k8sModel.Pod, namespace, prefix string, jobs map[string]struct{}, image string, containers []k8sModel.Container) string {
	main, ok := lo.Find(containers, func(c k8sModel.Container) bool { return c.Image == image })
	if !ok {
		return ""
	}

	var latest k8sModel.Pod
	digest := ""
	for _, pod := range pods {
		if _, own := jobs[jobprefix.JobName(pod.Labels)]; pod.Namespace != namespace || !own || !strings.HasPrefix(pod.Name, prefix) {
			continue
		}
		for _, ct := range pod.Containers {
			if d := imageref.DigestFromImageID(ct.ImageID); ct.Name == main.Name && d != "" && pod.StartedAt.After(latest.StartedAt) {
				latest, digest = pod, d
			}
		}
	}
	return digest
}
