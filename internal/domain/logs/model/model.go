package model

import "time"

// Line — строка лога с уже определённым уровнем.
type Line struct {
	TS    time.Time
	Level string // error | warn | info | debug | ""
	Text  string
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
}

// уровни логов
const (
	LevelError = "error"
	LevelWarn  = "warn"
	LevelInfo  = "info"
	LevelDebug = "debug"
)
