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
	Entities     []Entity
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

// Entity — бизнес-объекты по словам сервиса: сколько в каждом статусе и сколько застряло,
// поток за час (nil — сервис не считает).
type Entity struct {
	Name       string
	Status     string
	Statuses   []EntityStatus
	Created1h  *int64
	Finished1h *int64
}

type EntityStatus struct {
	Name   string
	Count  int64
	Stuck  int64
	Oldest time.Duration
}
