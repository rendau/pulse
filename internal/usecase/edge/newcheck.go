package edge

import (
	"context"
	"strconv"
	"time"

	"github.com/samber/lo"

	edgeService "github.com/rendau/pulse/internal/domain/edge/service"
)

// seenEntry — адрес встречался в журнале в промежутке [from, to): «не новый» для окна,
// которое начинается не раньше to и не позже from+lookback.
type seenEntry struct {
	from, to time.Time
}

type newResult struct {
	isNew   bool
	checked time.Duration // насколько назад от начала окна проверено
}

// checkNew — встречались ли адреса за lookback до начала окна. Журнал читается по суткам назад
// (сутки ≈8 с подстрочным фильтром): найденные адреса выбывают, проверка кончается, когда
// выбыли все, кончились lookback или бюджет. Не успели — new=true с меньшим checked.
func (r *run) checkNew(clients []*clientAgg, start time.Time) map[string]newResult {
	out := map[string]newResult{}
	lookback := r.u.conf.NewLookback
	oldest := start.Add(-lookback)

	pending := map[string]bool{}
	for _, c := range clients {
		if e, ok := r.u.seen[c.key]; ok && !e.to.After(start) && !e.from.Before(oldest) {
			out[c.key] = newResult{isNew: false, checked: lookback}
			continue
		}
		pending[c.key] = true
	}

	if len(pending) == 0 || !r.enough("новые адреса") {
		return out
	}
	budgetCtx, cancel := context.WithTimeout(r.ctx, min(r.u.conf.NewBudget, r.left()-time.Second))
	defer cancel()
	parent := r.ctx
	r.ctx = budgetCtx
	defer func() { r.ctx = parent }()

	dayEnd := start
	for len(pending) > 0 && dayEnd.After(oldest) {
		dayStart := lo.Latest(dayEnd.Add(-24*time.Hour), oldest)
		// адресов немного — грубый фильтр подстрокой (сутки ≈8 с), иначе — разбор JSON (≈15 с);
		// подстрока — адрес, у IPv6 — начало подсети /64; лишнее отсеивается по ключу
		keys := lo.Keys(pending)
		substrings := lo.Map(keys, func(key string, _ int) string { return activitySubstring(key) })
		samples, err := r.vector("sum by (ip) (count_over_time("+r.u.conf.Selector+prefilter(substrings)+" "+
			jsonFields(clientFields(nil))+" | "+ipLabel+" | ip=~"+strconv.Quote(keyRegex(keys))+" "+rangeOf(dayEnd.Sub(dayStart))+"))", dayEnd)
		if err != nil {
			// бюджет проверки кончился — это не ошибка сводки: у адресов checked меньше lookback
			if parent.Err() != nil || budgetCtx.Err() == nil {
				r.fail("новые адреса", err)
			}
			break
		}
		for _, s := range samples {
			key, ok := edgeService.ClientKey(s.Labels["ip"])
			if !ok || s.Value <= 0 || !pending[key] {
				continue
			}
			delete(pending, key)
			out[key] = newResult{isNew: false, checked: lookback}
			r.u.seen[key] = seenEntry{from: dayStart, to: dayEnd}
		}
		dayEnd = dayStart
	}

	for key := range pending {
		if checked := start.Sub(dayEnd); checked > 0 {
			out[key] = newResult{isNew: true, checked: checked}
		}
	}

	// промежуток старше lookback от «сейчас» не подходит уже ни одному окну по умолчанию
	for k, e := range r.u.seen {
		if time.Since(e.from) > lookback {
			delete(r.u.seen, k)
		}
	}
	return out
}
