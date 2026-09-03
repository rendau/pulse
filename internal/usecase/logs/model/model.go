package model

import (
	"time"

	logsModel "github.com/mechta-market/pulse/internal/domain/logs/model"
)

const (
	ModePatterns = "patterns"
	ModeRaw      = "raw"
)

// QueryReq — параметры query_logs.
type QueryReq struct {
	Service string
	Level   string // error | warn | info | debug | "" (все)
	Pattern string // регулярное выражение по строке (LogQL |~)
	Window  time.Duration
	Mode    string // patterns (по умолчанию) | raw
	Limit   int    // для raw, ≤ raw_limit
}

// QueryResult — результат: паттерны либо сырые строки.
type QueryResult struct {
	Service    string
	Selector   string
	Mode       string
	Start      time.Time
	End        time.Time
	TotalLines int
	// Truncated — Loki отдал не всё: строк за окно больше, чем max_lines
	Truncated bool
	Patterns  []logsModel.Pattern
	Lines     []logsModel.Line
}
