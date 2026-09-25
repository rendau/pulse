package model

import "time"

// статусы (итог и зависимости)
const (
	StatusOk       = "ok"
	StatusDegraded = "degraded"
	StatusDown     = "down"
)

// Status — ответ ручки состояния пода, проверенный и очищенный: только поля стандарта,
// тексты без учётных данных и персональных данных.
type Status struct {
	Status       string
	CheckedAt    time.Time
	Dependencies []Dependency
	Gauges       []Gauge
}

type Dependency struct {
	Id        string
	Status    string
	LatencyMs *int64
	Message   string
}

// Gauge — показатель, которого нет в метриках: число или время.
type Gauge struct {
	Id     string
	Title  string
	Value  *float64
	Time   *time.Time
	Unit   string
	Status string
}
