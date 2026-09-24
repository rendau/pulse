package logs

import (
	"context"
	"log/slog"
	"regexp"
	"sort"
	"strings"

	"github.com/samber/lo"

	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
	"github.com/mechta-market/pulse/internal/util/imageref"
)

// podGroup — чьи поды входят в логи сервиса: workload каталога (поды «имя-…») или Job'ы,
// которые создаёт оркестратор (loom, Argo Workflows и т.п.) — у них нет workload'а, но
// поды принадлежат сервису по app.kubernetes.io/managed-by или по репозиторию образа.
// Job'ы группируются по общему префиксу имени («lt-zeon-*»): в Loki только имя пода, а
// префикс покрывает и уже удалённые поды — логи отработавших Job'ов остаются доступны.
type podGroup struct {
	Name      string // имя workload'а или префикс Job'ов со звёздочкой
	Prefix    string // префикс имени пода
	Namespace string
	Jobs      bool
}

const (
	managedByLabel = "app.kubernetes.io/managed-by"
	maxJobGroups   = 10
)

// jobNameLabels — имя Job'а у его подов (новый и старый ключ Kubernetes).
var jobNameLabels = []string{"batch.kubernetes.io/job-name", "job-name"}

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
		if job := jobName(pod.Labels); job != "" && ownedBy(pod, service.Name) {
			jobs = append(jobs, job)
		} else {
			foreign = append(foreign, pod.Name)
		}
	}

	return append(groups, jobGroups(lo.Uniq(jobs), foreign, namespace)...)
}

func jobName(labels map[string]string) string {
	for _, key := range jobNameLabels {
		if name := labels[key]; name != "" {
			return name
		}
	}
	return ""
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

// jobGroups — общий префикс имён Job'ов до последнего «-» (lt-zeon-delivery-…, lt-zeon-product-…
// → «lt-zeon-*»). Префикс не должен захватывать чужие поды namespace'а (foreign) — иначе в логи
// сервиса попали бы чужие; тогда префикс на каждый Job (до maxJobGroups), конфликтные — отбрасываются.
func jobGroups(jobs, foreign []string, namespace string) []podGroup {
	if len(jobs) == 0 {
		return nil
	}
	sort.Strings(jobs)

	safe := func(prefix string) bool {
		return prefix != "" && !lo.ContainsBy(foreign, func(pod string) bool { return strings.HasPrefix(pod, prefix) })
	}

	prefixes := []string{cutToDash(commonPrefix(jobs))}
	if !safe(prefixes[0]) {
		prefixes = lo.Filter(lo.Uniq(lo.Map(jobs, func(job string, _ int) string { return cutToDash(job) })),
			func(p string, _ int) bool { return safe(p) })
		if len(prefixes) > maxJobGroups {
			prefixes = prefixes[:maxJobGroups]
		}
	}

	return lo.Map(prefixes, func(prefix string, _ int) podGroup {
		return podGroup{Name: prefix + "*", Prefix: prefix, Namespace: namespace, Jobs: true}
	})
}

func commonPrefix(values []string) string {
	prefix := values[0]
	for _, v := range values[1:] {
		for !strings.HasPrefix(v, prefix) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}

// cutToDash — префикс до последнего «-» включительно: имя Job'а оканчивается хэшем попытки.
func cutToDash(s string) string {
	if i := strings.LastIndex(s, "-"); i >= 0 {
		return s[:i+1]
	}
	return ""
}

// podRegex — регэксп имён подов для селектора: workload'ы — «^(a|b)-.*», с Job'ами —
// «^((a|b)-.*|lt-zeon-.*)».
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
