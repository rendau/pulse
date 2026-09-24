package logs

import (
	"context"
	"log/slog"
	"regexp"
	"strings"

	"github.com/samber/lo"

	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
	"github.com/mechta-market/pulse/internal/util/imageref"
	"github.com/mechta-market/pulse/internal/util/jobprefix"
)

// podGroup — чьи поды входят в логи сервиса: workload каталога (поды «имя-…») или Job'ы,
// которые создаёт оркестратор (Argo Workflows, Airflow и т.п.) — у них нет workload'а, но
// поды принадлежат сервису по app.kubernetes.io/managed-by или по репозиторию образа.
// Job'ы группируются по общему префиксу имени («nightly-*»): в Loki только имя пода, а
// префикс покрывает и уже удалённые поды — логи отработавших Job'ов остаются доступны.
type podGroup struct {
	Name      string // имя workload'а или префикс Job'ов со звёздочкой
	Prefix    string // префикс имени пода
	Namespace string
	Jobs      bool
}

const managedByLabel = "app.kubernetes.io/managed-by"

// podGroups — workload'ы сервиса и Job'ы оркестратора в его namespace. Не получилось
// прочитать поды — только workload'ы: Job'ы — уточнение, без них логи всё равно есть.
func (u *Usecase) podGroups(ctx context.Context, service *svcModel.Main, workloads []*workloadModel.Main) []podGroup {
	groups := lo.Map(workloadNames(workloads), func(name string, _ int) podGroup {
		return podGroup{Name: name, Prefix: name + "-", Namespace: workloads[0].Namespace}
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
		if lo.ContainsBy(groups, func(g podGroup) bool { return strings.HasPrefix(pod.Name, g.Prefix) }) {
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
		return podGroup{Name: prefix + "*", Prefix: prefix, Namespace: namespace, Jobs: true}
	})
}

// podRegex — регэксп имён подов для селектора: workload'ы — «^(a|b)-.*», с Job'ами —
// «^((a|b)-.*|nightly-.*)».
func podRegex(groups []podGroup) string {
	workloads := lo.FilterMap(groups, func(g podGroup, _ int) (string, bool) { return g.Name, !g.Jobs })
	jobs := lo.FilterMap(groups, func(g podGroup, _ int) (string, bool) { return regexp.QuoteMeta(g.Prefix) + ".*", g.Jobs })

	parts := jobs
	if len(workloads) > 0 {
		parts = append([]string{"(" + strings.Join(workloads, "|") + ")-.*"}, jobs...)
	}
	if len(parts) == 1 && len(workloads) > 0 {
		return "^" + parts[0]
	}
	return "^(" + strings.Join(parts, "|") + ")"
}

// groupOfPod — группа, к которой относится под: самый длинный префикс
// (sms-im-7d9f-q2 → sms-im, а не sms).
func groupOfPod(pod string, groups []podGroup) string {
	best := podGroup{}
	for _, g := range groups {
		if strings.HasPrefix(pod, g.Prefix) && len(g.Prefix) > len(best.Prefix) {
			best = g
		}
	}
	return best.Name
}
