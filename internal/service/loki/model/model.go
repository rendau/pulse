package model

import "time"

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
