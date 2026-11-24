package model

import (
	"errors"
	"time"
)

// ErrTooManySeries — метрический запрос дал больше серий, чем разрешает Loki (max_query_series):
// группировку надо сузить или разбить запрос на части.
var ErrTooManySeries = errors.New("loki: too many series")

// Stream — поток логов с набором лейблов и строками.
type Stream struct {
	Labels  map[string]string
	Entries []Entry
}

type Entry struct {
	TS   time.Time
	Line string
}

// Sample — значение метрического запроса (count_over_time) с лейблами группировки.
type Sample struct {
	Labels map[string]string
	Value  float64
}
