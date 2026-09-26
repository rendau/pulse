// Package podname — регэкспы имён подов workload'а по правилам именования Kubernetes.
// Префикс «имя-» захватывает чужие поды namespace'а: у сервиса pulse — поды pulse-agent-…,
// pulse-bot-…, pulse-pg-0. Регэксп по виду workload'а берёт только его поды:
//
//	Deployment   имя-<pod-template-hash>-<5 символов>
//	StatefulSet  имя-<номер>
//	DaemonSet    имя-<5 символов>
//	CronJob      имя-<время запуска в минутах>-<5 символов>
//	Job          имя-… (семейство Job'ов оркестратора: имя — общий префикс, jobprefix)
//
// Регэкспы — RE2 без обратных слешей: подставляются и в Loki, и в строки PromQL.
package podname

import (
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/samber/lo"

	"github.com/rendau/pulse/internal/constant"
)

const (
	// alnum — алфавит случайных суффиксов и pod-template-hash (rand.String и
	// rand.SafeEncodeString в k8s.io/apimachinery): без гласных, 0, 1 и 3 — поэтому
	// «agent», «bot» и т.п. в имени соседнего workload'а суффиксом не считаются.
	alnum = "[bcdfghjklmnpqrstvwxz2456789]"
	// suffix — случайный суффикс generateName
	suffix = alnum + "{5}"
	// maxGenerated — длина основы generateName: имя пода не длиннее 63, из них 5 — суффикс;
	// длинная основа обрезается (names.SimpleNameGenerator).
	maxGenerated = 63 - 5
)

// Workload — вид и имя workload'а.
type Workload struct {
	Kind string
	Name string
}

// Pattern — регэксп имён подов workload'а без якорей. Неизвестный вид — префикс «имя-».
func Pattern(kind, name string) string {
	switch kind {
	case constant.WorkloadKindDeployment:
		// ReplicaSet «имя-hash», хэш — 32-битное число в алфавите alnum (6–10 знаков)
		return generated(name+"-", alnum, 6, 10)
	case constant.WorkloadKindStatefulSet:
		return quote(name) + "-[0-9]+"
	case constant.WorkloadKindDaemonSet:
		return quote(truncate(name+"-")) + suffix
	case constant.WorkloadKindCronJob:
		// Job «имя-<время запуска в минутах от эпохи>»
		return generated(name+"-", "[0-9]", 1, 10)
	default:
		return Prefix(name + "-")
	}
}

// Prefix — регэксп имён с префиксом без якорей: Job'ы оркестратора (jobprefix).
func Prefix(prefix string) string {
	return quote(prefix) + ".*"
}

// ObjectPattern — регэксп имён объектов workload'а без якорей: он сам, его ReplicaSet'ы
// или Job'ы и поды (объекты Warning-событий).
func ObjectPattern(kind, name string) string {
	parts := []string{quote(name)}
	switch kind {
	case constant.WorkloadKindDeployment:
		parts = append(parts, quote(name)+"-"+alnum+"{6,10}")
	case constant.WorkloadKindCronJob:
		parts = append(parts, quote(name)+"-[0-9]{1,10}")
	}
	return "(?:" + strings.Join(append(parts, Pattern(kind, name)), "|") + ")"
}

// Regex — регэксп имён подов набора workload'ов для {pod_regex}: «^(?:a|b)$».
func Regex(workloads []Workload) string {
	return Join(lo.Map(workloads, func(w Workload, _ int) string { return Pattern(w.Kind, w.Name) }))
}

// Join — альтернатива регэкспов без якорей, с якорями по краям.
func Join(patterns []string) string {
	patterns = lo.Uniq(patterns)
	if len(patterns) == 1 {
		return "^" + patterns[0] + "$"
	}
	return "^(?:" + strings.Join(patterns, "|") + ")$"
}

// Match — под принадлежит workload'у.
func Match(kind, name, pod string) bool {
	return MatchPattern(Pattern(kind, name), pod)
}

// MatchObject — объект (сам workload, ReplicaSet, Job, под) принадлежит workload'у.
func MatchObject(kind, name, object string) bool {
	return MatchPattern(ObjectPattern(kind, name), object)
}

// Owner — индекс workload'а, которому принадлежит под: по правилам именования (из
// нескольких — с самым длинным именем, семейство Job'ов — префикс), иначе — с самым длинным
// префиксом «имя-» (под вне правил: Job, созданный вручную из CronJob); -1 — ничей.
func Owner(pod string, workloads []Workload) int {
	return owner(pod, workloads, Match)
}

// ObjectOwner — Owner для объекта Warning-события (workload, ReplicaSet, Job, под).
func ObjectOwner(object string, workloads []Workload) int {
	return owner(object, workloads, func(kind, name, object string) bool {
		return object == name || MatchObject(kind, name, object)
	})
}

func owner(value string, workloads []Workload, match func(kind, name, value string) bool) int {
	best, byPrefix := -1, -1
	for i, w := range workloads {
		if match(w.Kind, w.Name, value) && (best < 0 || len(w.Name) > len(workloads[best].Name)) {
			best = i
		}
		if strings.HasPrefix(value, w.Name+"-") && (byPrefix < 0 || len(w.Name) > len(workloads[byPrefix].Name)) {
			byPrefix = i
		}
	}
	if best >= 0 {
		return best
	}
	return byPrefix
}

// compiled — скомпилированные регэкспы по шаблону: сопоставление идёт в циклах по подам.
var compiled sync.Map

// MatchPattern — имя целиком соответствует регэкспу Pattern/ObjectPattern.
func MatchPattern(pattern, value string) bool {
	re, ok := compiled.Load(pattern)
	if !ok {
		re, _ = compiled.LoadOrStore(pattern, regexp.MustCompile("^(?:"+pattern+")$"))
	}
	return re.(*regexp.Regexp).MatchString(value)
}

// generated — имя пода от generateName «prefix<середина>-»: середина — от min до max
// символов class; основа длиннее maxGenerated обрезается вместе с серединой и её «-».
func generated(prefix, class string, minLen, maxLen int) string {
	if len(prefix) >= maxGenerated {
		return quote(truncate(prefix)) + suffix
	}
	room := maxGenerated - len(prefix)
	if room > maxLen {
		return quote(prefix) + repeat(class, minLen, maxLen) + "-" + suffix
	}
	// середина с «-» влезает целиком, либо обрезана до room символов
	alternatives := []string{}
	if room-1 >= minLen {
		alternatives = append(alternatives, repeat(class, minLen, room-1)+"-")
	}
	alternatives = append(alternatives, repeat(class, room, room))
	return quote(prefix) + "(?:" + strings.Join(alternatives, "|") + ")" + suffix
}

func truncate(s string) string {
	if len(s) > maxGenerated {
		return s[:maxGenerated]
	}
	return s
}

func repeat(class string, minLen, maxLen int) string {
	if minLen == maxLen {
		return class + "{" + strconv.Itoa(minLen) + "}"
	}
	return class + "{" + strconv.Itoa(minLen) + "," + strconv.Itoa(maxLen) + "}"
}

// quote — имя как литерал: в именах Kubernetes из метасимволов бывает только точка,
// «[.]» вместо «\.» — обратный слеш в строке PromQL пришлось бы удваивать.
func quote(s string) string {
	return strings.ReplaceAll(regexp.QuoteMeta(s), `\.`, "[.]")
}
