package model

import "time"

// Line — строка лога с уже определённым уровнем.
type Line struct {
	TS       time.Time
	Level    string // error | warn | info | debug | ""
	Text     string
	Workload string // workload сервиса, из пода которого строка; пусто — не определён
	// Service, Namespace — чья строка при поиске по всем сервисам (Service пусто — под не из каталога)
	Service   string
	Namespace string
}

// Pattern — группа одинаковых по шаблону строк: тысяча одинаковых ошибок приходит
// к модели как одна строка со счётчиком.
type Pattern struct {
	Count     int
	Level     string
	Template  string
	Example   string
	FirstSeen time.Time
	LastSeen  time.Time
	Workloads []string // из каких workload'ов сервиса строки паттерна
	Services  []string // из каких сервисов строки паттерна (поиск по всем сервисам)
}

// ServiceHits — сколько найденных строк у одного сервиса (поиск по всем сервисам).
type ServiceHits struct {
	Service   string // пусто — под не из каталога, тогда владелец — Namespace
	Namespace string
	Count     int
	FirstSeen time.Time
	LastSeen  time.Time
}

// ClusterErrors — выжимка error-строк по всем логам кластера за окно.
type ClusterErrors struct {
	Window time.Duration
	Total  int // error-строк за окно во всём кластере (счётчик Loki)
	// Services — сервисы с наибольшим числом ошибок; ServicesTotal — сколько их всего
	Services      []ServiceErrors
	ServicesTotal int
}

// ServiceErrors — error-строки одного сервиса (или namespace'а, если под не из каталога).
type ServiceErrors struct {
	Service   string
	Namespace string
	Count     int
	// Top — самый частый паттерн в выборке строк; Count == 0, если в выборку строки сервиса не попали
	Top Pattern
}

// уровни логов
const (
	LevelError = "error"
	LevelWarn  = "warn"
	LevelInfo  = "info"
	LevelDebug = "debug"
)
