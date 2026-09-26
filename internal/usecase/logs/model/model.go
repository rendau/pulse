package model

import (
	"time"

	logsModel "github.com/mechta-market/pulse/internal/domain/logs/model"
)

const (
	ModePatterns = "patterns"
	ModeRaw      = "raw"
)

// откуда строки: Loki (история за окно) или Kubernetes API (только живые поды — запасной
// источник, когда Loki не подключён или недоступен)
const (
	SourceLoki       = "loki"
	SourceKubernetes = "kubernetes"
)

// QueryReq — параметры query_logs.
type QueryReq struct {
	// Service — пусто: поиск Pattern во всех логах кластера (номер заказа, id клиента)
	Service string
	Level   string // error | warn | info | debug | "" (все)
	Pattern string // регулярное выражение по строке (LogQL |~)
	Window  time.Duration
	Mode    string // patterns (по умолчанию) | raw
	Limit   int    // для raw, ≤ raw_limit
	// Workload — только логи одного workload'а сервиса (notifire-sms у сервиса sms); пусто — все
	Workload string
	// End — конец окна (пусто — сейчас); EndIsDay — задан днём: окно по умолчанию — эти сутки
	End      time.Time
	EndIsDay bool
}

// почему поиск назад по дням остановился
const (
	SearchStopFound     = "found"     // следы найдены, а перед ними дни без совпадений
	SearchStopBudget    = "budget"    // кончилось время на поиск: проверено только с Start
	SearchStopRetention = "retention" // дошёл до конца хранения логов
	SearchStopLimit     = "limit"     // строк больше лимита выборки
)

// QueryResult — результат: паттерны либо сырые строки.
type QueryResult struct {
	Service    string
	Selector   string
	Source     string // SourceLoki | SourceKubernetes
	Mode       string
	Start      time.Time
	End        time.Time
	TotalLines int
	// Truncated — Loki отдал не всё: строк за окно больше, чем max_lines
	Truncated bool
	Patterns  []logsModel.Pattern
	Lines     []logsModel.Line
	// Services — поиск по всем сервисам: сколько строк нашлось у каждого
	Services []logsModel.ServiceHits
	// SearchStop — поиск назад по дням (SearchStop*); пусто — искали в одном окне
	SearchStop string
	// IdMatches — поиск по всем сервисам: чьим объектом может быть искомый номер (формат номера
	// из манифестов, раздел domain)
	IdMatches []IdMatch
}

// IdMatch — искомый номер подходит под формат номера объекта сервиса.
type IdMatch struct {
	Service string
	Entity  string
}
