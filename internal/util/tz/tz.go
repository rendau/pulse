// Package tz — часовой пояс, в котором pulse показывает время в ответах инструментов.
package tz

import (
	"fmt"
	"time"
	// база часовых поясов вшита в бинарник: не зависим от tzdata образа и env TZ
	_ "time/tzdata"
)

const Name = "Asia/Almaty"

var Location = mustLoad(Name)

// In переводит время в часовой пояс ответов; нулевое время не трогает,
// чтобы оно оставалось 0001-01-01T00:00:00Z, а не LMT-смещением.
func In(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.In(Location)
}

// InPtr — In для необязательного времени.
func InPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	return new(In(*t))
}

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic("tz: " + err.Error())
	}
	return loc
}

// endLayouts — момент в поясе ответов: с секундами, без них, через пробел.
var endLayouts = []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04"}

// ParseEnd разбирает конец окна запроса: дата «2026-09-20» — конец этих суток (day = true:
// окно по умолчанию — весь день), время «2026-09-20T16:00» — в поясе ответов, RFC3339 —
// как есть.
func ParseEnd(s string) (end time.Time, day bool, err error) {
	if d, err := time.ParseInLocation(time.DateOnly, s, Location); err == nil {
		return d.AddDate(0, 0, 1), true, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, false, nil
	}
	for _, layout := range endLayouts {
		if t, err := time.ParseInLocation(layout, s, Location); err == nil {
			return t, false, nil
		}
	}
	return time.Time{}, false, fmt.Errorf("end %q: expected date 2026-09-20 or time 2026-09-20T16:00", s)
}
