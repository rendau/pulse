// Package tz — часовой пояс, в котором pulse показывает время в ответах инструментов.
package tz

import (
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
