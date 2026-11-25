package edge

import (
	"context"
	"strconv"
	"time"

	"github.com/samber/lo"

	edgeService "github.com/rendau/pulse/internal/domain/edge/service"
)

// Проверка «новый ли адрес» — самое тяжёлое место сводки: у Loki нет индекса по словам, и сутки
// журнала — это ~6 ГБ строк (~10 с на всех ядрах Loki) при любом числе адресов в запросе. Поэтому
// pulse помнит оба ответа: «адрес встречался» (seen) и «адреса не было в таком-то промежутке»
// (absent). Повторная проверка того же адреса досматривает только журнал после прошлой проверки
// (минуты) и, если прошлая не дошла до lookback, — глубже неё. Без absent адрес, которого
// действительно не было, перечитывал неделю журнала при каждом опросе, пока держался в топе.

// seenEntry — адрес встречался в журнале в промежутке [from, to): «не новый» для окна,
// которое начинается не раньше to и не позже from+lookback.
type seenEntry struct {
	from, to time.Time
}

// absentEntry — адреса не было в журнале в промежутке [from, to): проверено.
type absentEntry struct {
	from, to time.Time
}

// span — промежуток журнала [from, to), который у адреса ещё не проверен.
type span struct {
	from, to time.Time
}

type newResult struct {
	isNew   bool
	checked time.Duration // насколько назад от начала окна проверено
}

// checkNew — встречались ли адреса за lookback до начала окна. Журнал читается отрезками не
// длиннее суток сверху вниз, только там, где у кого-то из адресов он ещё не проверен: найденные
// адреса выбывают, проверка кончается, когда проверено всё, или кончился бюджет. Не успели —
// new=true с меньшим checked, проверенное запоминается для следующего вызова.
func (r *run) checkNew(clients []*clientAgg, start time.Time) map[string]newResult {
	out := map[string]newResult{}
	lookback := r.u.conf.NewLookback
	oldest := start.Add(-lookback)
	defer r.u.forgetOld(lookback)

	todo := map[string][]span{}
	for _, c := range clients {
		if e, ok := r.u.seen[c.key]; ok && !e.to.After(start) && !e.from.Before(oldest) {
			out[c.key] = newResult{isNew: false, checked: lookback}
			continue
		}
		todo[c.key] = r.u.unchecked(c.key, oldest, start)
	}

	// непроверенного нет ни у кого (проверены раньше) — Loki не нужен
	if !lo.SomeBy(lo.Values(todo), func(s []span) bool { return len(s) > 0 }) || !r.enough("новые адреса") {
		r.finishNew(todo, out, oldest, start)
		return out
	}
	budgetCtx, cancel := context.WithTimeout(r.ctx, min(r.u.conf.NewBudget, r.left()-time.Second))
	defer cancel()
	parent := r.ctx
	r.ctx = budgetCtx
	defer func() { r.ctx = parent }()

	cursor := start
	for {
		segStart, segEnd, ok := nextSegment(todo, cursor)
		if !ok {
			break
		}
		keys := lo.Filter(lo.Keys(todo), func(k string, _ int) bool { return overlaps(todo[k], segStart, segEnd) })
		// адресов немного — грубый фильтр подстрокой, иначе — разбор JSON; подстрока — адрес, у
		// IPv6 — начало подсети /64; лишнее отсеивается по ключу
		substrings := lo.Map(keys, func(key string, _ int) string { return activitySubstring(key) })
		samples, err := r.vector("sum by (ip) (count_over_time("+r.u.conf.Selector+prefilter(substrings)+" "+
			jsonFields(clientFields(nil))+" | "+ipLabel+" | ip=~"+strconv.Quote(keyRegex(keys))+" "+rangeOf(segEnd.Sub(segStart))+"))", segEnd)
		if err != nil {
			// бюджет проверки кончился — это не ошибка сводки: у адресов checked меньше lookback
			if parent.Err() != nil || budgetCtx.Err() == nil {
				r.fail("новые адреса", err)
			}
			break
		}
		found := map[string]bool{}
		for _, s := range samples {
			if key, ok := edgeService.ClientKey(s.Labels["ip"]); ok && s.Value > 0 {
				found[key] = true
			}
		}
		for _, key := range keys {
			if !found[key] {
				todo[key] = subtract(todo[key], segStart, segEnd)
				continue
			}
			delete(todo, key)
			delete(r.u.absent, key)
			out[key] = newResult{isNew: false, checked: lookback}
			r.u.seen[key] = seenEntry{from: segStart, to: segEnd}
		}
		cursor = segStart
	}

	r.finishNew(todo, out, oldest, start)
	return out
}

// finishNew — итог по адресам, которых в проверенном журнале не нашлось: проверено всё выше
// самого верхнего непроверенного промежутка; это запоминается для следующих вызовов.
func (r *run) finishNew(todo map[string][]span, out map[string]newResult, oldest, start time.Time) {
	for key, spans := range todo {
		covered := oldest
		if len(spans) > 0 {
			covered = spans[0].to
		}
		if checked := start.Sub(covered); checked > 0 {
			out[key] = newResult{isNew: true, checked: checked}
		}
		r.u.rememberAbsent(key, covered, start)
	}
}

// unchecked — что журнала [oldest, start) у адреса ещё не проверено, сверху вниз: после прошлой
// проверки и глубже неё.
func (u *Usecase) unchecked(key string, oldest, start time.Time) []span {
	e, ok := u.absent[key]
	// окно раньше прошлой проверки (end в прошлом) — проверяется заново целиком
	if !ok || e.to.After(start) {
		return []span{{from: oldest, to: start}}
	}
	var spans []span
	if e.to.Before(start) {
		spans = append(spans, span{from: lo.Latest(e.to, oldest), to: start})
	}
	if e.from.After(oldest) {
		spans = append(spans, span{from: oldest, to: e.from})
	}
	return spans
}

// rememberAbsent — адреса не было в [from, to). Примыкает к прошлому промежутку — склеиваются;
// не примыкает (не успели досмотреть журнал после прошлой проверки) — остаётся прошлый: в
// следующий раз он досмотрится.
func (u *Usecase) rememberAbsent(key string, from, to time.Time) {
	if !from.Before(to) {
		return
	}
	if e, ok := u.absent[key]; ok {
		if e.to.Before(from) {
			return
		}
		from = lo.Earliest(from, e.from)
	}
	u.absent[key] = absentEntry{from: from, to: to}
}

// forgetOld — промежутки старше lookback от «сейчас» не подходят уже ни одному окну по умолчанию.
func (u *Usecase) forgetOld(lookback time.Duration) {
	for k, e := range u.seen {
		if time.Since(e.from) > lookback {
			delete(u.seen, k)
		}
	}
	for k, e := range u.absent {
		if time.Since(e.to) > lookback {
			delete(u.absent, k)
		}
	}
}

// maxSegment — отрезок журнала на один запрос: сутки — ~6 ГБ, ~10 с
const maxSegment = 24 * time.Hour

// nextSegment — следующий отрезок журнала ниже cursor: сверху — самый поздний непроверенный
// момент, снизу — не глубже суток и не глубже начала непроверенных промежутков, которые его
// содержат. Так досмотр после прошлой проверки — короткий запрос на минуты, а не на сутки.
func nextSegment(todo map[string][]span, cursor time.Time) (from, to time.Time, ok bool) {
	for _, spans := range todo {
		for _, s := range spans {
			if s.from.Before(cursor) {
				if end := lo.Earliest(s.to, cursor); !ok || end.After(to) {
					to, ok = end, true
				}
			}
		}
	}
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	from = to
	for _, spans := range todo {
		for _, s := range spans {
			if s.from.Before(to) && !s.to.Before(to) && s.from.Before(from) {
				from = s.from
			}
		}
	}
	return lo.Latest(from, to.Add(-maxSegment)), to, true
}

// overlaps — есть ли в непроверенном отрезок [from, to).
func overlaps(spans []span, from, to time.Time) bool {
	return lo.SomeBy(spans, func(s span) bool { return s.from.Before(to) && s.to.After(from) })
}

// subtract — непроверенное без проверенного отрезка [from, to), порядок сверху вниз сохраняется.
func subtract(spans []span, from, to time.Time) []span {
	var out []span
	for _, s := range spans {
		if s.to.After(to) {
			out = append(out, span{from: lo.Latest(s.from, to), to: s.to})
		}
		if s.from.Before(from) {
			out = append(out, span{from: s.from, to: lo.Earliest(s.to, from)})
		}
	}
	return out
}
