// Package jobprefix — префиксы имён подов для Job'ов, которые создаёт оркестратор (Argo
// Workflows, Airflow и т.п.): у таких Job'ов нет workload'а, а имена уникальны на каждую
// попытку. Общий префикс («nightly-sync-*») покрывает и будущие, и уже удалённые поды —
// в Loki от пода остаётся только имя.
package jobprefix

import (
	"sort"
	"strings"

	"github.com/samber/lo"
)

// MaxGroups — потолок префиксов, когда общий не годится.
const MaxGroups = 10

// Prefixes — префиксы имён для набора Job'ов: общий префикс до последнего «-»; если он
// захватывает чужие поды namespace'а (foreign) — префикс на каждое семейство Job'ов
// (имя без хэша попытки), конфликтные отбрасываются.
func Prefixes(jobs, foreign []string) []string {
	if len(jobs) == 0 {
		return nil
	}
	jobs = lo.Uniq(jobs)
	sort.Strings(jobs)

	safe := func(prefix string) bool {
		return prefix != "" && !lo.ContainsBy(foreign, func(pod string) bool { return strings.HasPrefix(pod, prefix) })
	}

	if common := CutToDash(commonPrefix(jobs)); safe(common) {
		return []string{common}
	}

	prefixes := lo.Filter(lo.Uniq(lo.Map(jobs, func(job string, _ int) string { return CutToDash(job) })),
		func(p string, _ int) bool { return safe(p) })
	if len(prefixes) > MaxGroups {
		prefixes = prefixes[:MaxGroups]
	}
	return prefixes
}

// CutToDash — префикс до последнего «-» включительно: имя Job'а оканчивается хэшем попытки.
func CutToDash(s string) string {
	if i := strings.LastIndex(s, "-"); i >= 0 {
		return s[:i+1]
	}
	return ""
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

// JobName — имя Job'а у его пода (новый и старый ключ Kubernetes).
func JobName(labels map[string]string) string {
	for _, key := range []string{"batch.kubernetes.io/job-name", "job-name"} {
		if name := labels[key]; name != "" {
			return name
		}
	}
	return ""
}
