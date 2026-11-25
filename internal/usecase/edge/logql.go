package edge

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/samber/lo"

	edgeService "github.com/rendau/pulse/internal/domain/edge/service"
)

// LogQL журнала входящих запросов. Порядок стадий важен для нагрузки на Loki: сначала грубый
// фильтр строк подстрокой (|= — в разы быстрее регэкспа и разбора JSON), потом | json только
// нужных полей, потом фильтры по полям.

// ipLabel — лейбл ip: адрес клиента из заголовка Cloudflare, иначе адрес соединения. Адреса
// IPv6 к подсети /64 приводит pulse (edgeService.ClientKey): regexReplaceAll в label_format
// втрое замедляет запрос (час журнала — 9 с против 3 с).
const ipLabel = `label_format ip=` + "`" + `{{ or .cf .ch }}` + "`"

// statusClassLabel — лейбл sc: первая цифра кода ответа (2, 4, 5…)
const statusClassLabel = `sc=` + "`" + `{{ substr 0 1 .status }}` + "`"

// jsonFields — стадия | json: лейбл = поле строки. Поля с дефисом — в скобочной записи.
func jsonFields(fields map[string]string) string {
	keys := slices.Sorted(maps.Keys(fields))
	// строки не из журнала запросов (не JSON) — мимо: иначе их лейбл __error_details__ (у каждой
	// свой) даёт сотни серий
	return "| json " + strings.Join(lo.Map(keys, func(label string, _ int) string {
		field := fields[label]
		if strings.Contains(field, "-") {
			return label + "=" + "`" + `["` + field + `"]` + "`"
		}
		return label + "=" + strconv.Quote(field)
	}), ", ") + ` | __error__=""`
}

// clientFields — поля адреса клиента для ipLabel.
func clientFields(extra map[string]string) map[string]string {
	fields := map[string]string{"ch": edgeService.FieldClientHost, "cf": edgeService.FieldCfClientIp}
	maps.Copy(fields, extra)
	return fields
}

// base — селектор журнала и грубые фильтры: только внешняя дверь (internal=false) и домен.
func (u *Usecase) base(host string, internal bool) string {
	q := u.conf.Selector
	if !internal && u.conf.InternalRouterPrefix != "" {
		q += " != " + strconv.Quote(`"`+edgeService.FieldRouter+`":"`+u.conf.InternalRouterPrefix)
	}
	if host != "" {
		q += " |= " + strconv.Quote(`"`+edgeService.FieldHost+`":"`+host+`"`)
	}
	return q
}

// anyOf — фильтр строк «содержит любую из подстрок» (|= "a" or "b").
func anyOf(substrings []string) string {
	if len(substrings) == 0 {
		return ""
	}
	return " |= " + strings.Join(lo.Map(substrings, func(s string, _ int) string { return strconv.Quote(s) }), " or ")
}

// clientSubstrings — подстроки строки журнала с адресами (лейблами ip из Loki): в кавычках —
// 1.2.3.4 не находится в 11.2.3.45.
func clientSubstrings(labels []string) []string {
	return lo.Uniq(lo.Map(labels, func(l string, _ int) string { return `"` + l + `"` }))
}

// ipIn — фильтр лейбла ip по списку значений.
func ipIn(labels []string) string {
	return " | ip=~" + strconv.Quote("^(?:"+strings.Join(lo.Map(labels, func(l string, _ int) string {
		return regexp.QuoteMeta(l)
	}), "|")+")$")
}

// rangeOf — длительность окна в записи LogQL.
func rangeOf(d time.Duration) string {
	return fmt.Sprintf("[%ds]", int64(d/time.Second))
}

// shard — часть адресов по окончанию лейбла ip (Loki не отдаёт больше max_query_series серий,
// а разных адресов за час — тысячи): suffix — классы символов с конца адреса; other — адреса
// иного вида (пустые). Час журнала — ~15 частей по ~350 адресов.
type shard struct {
	suffix []string
	other  bool
}

// initialShards — первое разбиение по окончанию адреса: у IPv4 это цифра, у IPv6 — ещё a-f
// (реже — парами) или «::» (краулеры раздают тысячи адресов вида 2a03:2880:f812:7b::).
func initialShards() []shard {
	classes := []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "[ab]", "[cd]", "[ef]", "[0-7]::", "[89a-f]::"}
	shards := lo.Map(classes, func(c string, _ int) shard { return shard{suffix: []string{c}} })
	return append(shards, shard{other: true})
}

// splitClasses — предыдущий символ адреса тремя группами: часть, переполненная немного (а так
// обычно и бывает), делится на три запроса, а не на восемнадцать по каждому символу
var splitClasses = []string{"[0-4]", "[5-9]", "[^0-9]"}

// split — часть, всё ещё больше лимита, делится по предыдущему символу.
func (s shard) split() []shard {
	return lo.Map(splitClasses, func(c string, _ int) shard {
		return shard{suffix: append([]string{c}, s.suffix...)}
	})
}

func (s shard) filter() string {
	if s.other {
		return ` | ip!~"^.*(?:[0-9a-f]|::)$"`
	}
	return " | ip=~" + strconv.Quote("^.*"+strings.Join(s.suffix, "")+"$")
}

// keyRegex — регэксп лейбла ip по ключам клиентов: IPv4 — точно, подсеть IPv6 /64 — начало
// адреса (до «::» сжатой записи; лишнее отсеивается по ключу в pulse).
func keyRegex(keys []string) string {
	return "^(?:" + strings.Join(lo.Map(keys, func(k string, _ int) string {
		if base, ok := strings.CutSuffix(k, "/64"); ok {
			base, _, _ = strings.Cut(base, "::")
			return regexp.QuoteMeta(base+":") + ".*"
		}
		return regexp.QuoteMeta(k)
	}), "|") + ")$"
}

// maxPrefilterSubstrings — больше подстрок в «|= a or b …» Loki проверяет дольше, чем разбирает
// JSON целиком: 25 адресов — 7 с на час журнала против 3 с без предфильтра
const maxPrefilterSubstrings = 6

// prefilter — грубый фильтр строк по подстрокам, если их немного.
func prefilter(substrings []string) string {
	if len(substrings) > maxPrefilterSubstrings {
		return ""
	}
	return anyOf(substrings)
}

// signatureLabel — лейбл sig: совпавший с признаком взлома кусок пути (имя признака по нему
// называет edgeService.MatchSignatures).
func signatureLabel() string {
	re := `^.*?` + strings.TrimPrefix(edgeService.SignaturesRegex(), "(?i)") + `.*$`
	return "sig=`{{ regexReplaceAll " + strconv.Quote("(?i)"+re) + " .path \"${1}\" }}`"
}

// signatureOrEmptyLabel — лейбл sig: совпавший с признаком взлома кусок пути, иначе пусто (в
// общем запросе с чувствительными ручками проходят и пути без признака). Вторая ветка регэкспа
// ловит любой путь без признака и даёт пустую первую группу.
func signatureOrEmptyLabel() string {
	re := `^(?:.*?` + strings.TrimPrefix(edgeService.SignaturesRegex(), "(?i)") + `.*|.*)$`
	return "sig=`{{ regexReplaceAll " + strconv.Quote("(?is)"+re) + " .path \"${1}\" }}`"
}

// signatureFilter — путь с признаком взлома.
func signatureFilter() string {
	return " | path=~" + strconv.Quote(`(?i)^.*`+strings.TrimPrefix(edgeService.SignaturesRegex(), "(?i)")+`.*$`)
}

// sensitiveLabel — лейбл ep: первая (по порядку конфига) чувствительная ручка, чья часть есть в
// пути; нет — пусто.
func sensitiveLabel(patterns []string) string {
	var b strings.Builder
	for i, p := range patterns {
		if i > 0 {
			b.WriteString("{{ else if ")
		} else {
			b.WriteString("{{ if ")
		}
		b.WriteString("contains " + strconv.Quote(p) + " .path }}" + p)
	}
	b.WriteString("{{ end }}")
	return "ep=`" + b.String() + "`"
}

// pathIdLabel — лейбл np: путь, где сегменты с цифрами склеены в {id} (для числа разных путей).
const pathIdLabel = "np=`{{ regexReplaceAll \"/[^/]*[0-9][^/]*\" .path \"/{id}\" }}`"
