package logs

import (
	"context"
	"log/slog"
	"strings"

	"github.com/samber/lo"

	svcModel "github.com/rendau/pulse/internal/domain/svc/model"
	workloadModel "github.com/rendau/pulse/internal/domain/workload/model"
	k8sModel "github.com/rendau/pulse/internal/service/k8s/model"
	"github.com/rendau/pulse/internal/util/imageref"
	"github.com/rendau/pulse/internal/util/jobprefix"
	"github.com/rendau/pulse/internal/util/podname"
)

// podGroup — чьи поды входят в логи сервиса: workload каталога (поды по правилам именования
// его вида — podname: префикс «имя-» захватил бы соседей, pulse-agent-… у pulse) или Job'ы,
// которые создаёт оркестратор (Argo Workflows, Airflow и т.п.) — у них нет workload'а, но
// поды принадлежат сервису по app.kubernetes.io/managed-by или по репозиторию образа.
// Job'ы группируются по общему префиксу имени («nightly-*»): в Loki только имя пода, а
// префикс покрывает и уже удалённые поды — логи отработавших Job'ов остаются доступны.
type podGroup struct {
	Name      string // имя workload'а или префикс Job'ов со звёздочкой
	Pattern   string // регэксп имён подов без якорей (podname)
	Namespace string
	Jobs      bool
}

const managedByLabel = "app.kubernetes.io/managed-by"

// podGroups — workload'ы сервиса и Job'ы оркестратора в его namespace. Не получилось
// прочитать поды — только workload'ы: Job'ы — уточнение, без них логи всё равно есть.
func (u *Usecase) podGroups(ctx context.Context, service *svcModel.Main, workloads []*workloadModel.Main) []podGroup {
	groups := lo.Map(workloadKinds(workloads), func(w podname.Workload, _ int) podGroup {
		return podGroup{Name: w.Name, Pattern: podname.Pattern(w.Kind, w.Name), Namespace: workloads[0].Namespace}
	})
	if len(workloads) == 0 || u.k8s == nil || strings.TrimSpace(service.Metadata.Logs.Selector) != "" {
		return groups
	}

	namespace := workloads[0].Namespace
	pods, err := u.k8s.ListPods(ctx, namespace, "")
	if err != nil {
		slog.Warn("logs: list pods for job groups", "service", service.Name, "namespace", namespace, "error", err)
		return groups
	}

	var jobs, foreign []string
	for _, pod := range pods {
		if lo.ContainsBy(groups, func(g podGroup) bool { return podname.MatchPattern(g.Pattern, pod.Name) }) {
			continue // под workload'а сервиса
		}
		if job := jobprefix.JobName(pod.Labels); job != "" && ownedBy(pod, service.Name) {
			jobs = append(jobs, job)
		} else {
			foreign = append(foreign, pod.Name)
		}
	}

	return append(groups, jobGroups(lo.Uniq(jobs), foreign, namespace)...)
}

// ownedBy — под принадлежит сервису: его создал оркестратор-сервис (managed-by) или он
// запущен из образа репозитория сервиса.
func ownedBy(pod k8sModel.Pod, service string) bool {
	if pod.Labels[managedByLabel] == service {
		return true
	}
	return lo.ContainsBy(pod.Containers, func(ct k8sModel.PodContainer) bool {
		ref, err := imageref.Parse(ct.Image)
		return err == nil && ref.RepoName() == service
	})
}

// jobGroups — группы Job'ов по префиксам имён (jobprefix.Prefixes).
func jobGroups(jobs, foreign []string, namespace string) []podGroup {
	return lo.Map(jobprefix.Prefixes(jobs, foreign), func(prefix string, _ int) podGroup {
		return podGroup{Name: prefix + "*", Pattern: podname.Prefix(prefix), Namespace: namespace, Jobs: true}
	})
}

// podRegex — регэксп имён подов для селектора: поды workload'ов (podname) и Job'ов
// оркестратора по префиксу — «^(?:a-…|nightly-.*)$».
func podRegex(groups []podGroup) string {
	return podname.Join(lo.Map(groups, func(g podGroup, _ int) string { return g.Pattern }))
}

// groupOfPod — группа, к которой относится под: workload по правилам именования, иначе
// Job'ы с самым длинным префиксом (nightly-sync-1-… → nightly-sync-1-*, а не nightly-*).
func groupOfPod(pod string, groups []podGroup) string {
	best := podGroup{}
	for _, g := range groups {
		if !podname.MatchPattern(g.Pattern, pod) {
			continue
		}
		if !g.Jobs {
			return g.Name
		}
		if len(g.Pattern) > len(best.Pattern) {
			best = g
		}
	}
	return best.Name
}
