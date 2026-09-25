package dto

import (
	"time"

	"github.com/samber/lo"

	logsModel "github.com/mechta-market/pulse/internal/domain/logs/model"
	usecaseLogsModel "github.com/mechta-market/pulse/internal/usecase/logs/model"
	"github.com/mechta-market/pulse/internal/util/tz"
)

// query_logs

type QueryLogsReq struct {
	Service string `json:"service,omitempty" jsonschema:"точное имя сервиса; пусто — поиск pattern во всех логах кластера (номер заказа, id клиента)"`
	Level   string `json:"level,omitempty" jsonschema:"error | warn | info | debug; пусто — все уровни"`
	Pattern string `json:"pattern,omitempty" jsonschema:"регулярное выражение по строке (RE2), например acquirer.*timeout"`
	Window  string `json:"window,omitempty" jsonschema:"Go duration: 15m, 1h (по умолчанию; без service — 24h), 24h (максимум по умолчанию)"`
	Mode    string `json:"mode,omitempty" jsonschema:"patterns (по умолчанию; без service — raw) — агрегированные паттерны со счётчиком; raw — последние строки"`
	Limit   int    `json:"limit,omitempty" jsonschema:"для raw: число строк, максимум 100"`
	// Workload — сузить до одного workload'а сервиса
	Workload string `json:"workload,omitempty" jsonschema:"только логи одного workload'а сервиса (имя из workloads в get_service_info, например notifire-sms); пусто — все"`
}

type QueryLogsRep struct {
	Service    string       `json:"service"`
	Selector   string       `json:"selector" jsonschema:"LogQL-селектор, по которому выбраны логи (для kubernetes — поды)"`
	Source     string       `json:"source" jsonschema:"loki — история за окно; kubernetes — Loki нет или недоступен: только живые поды, хвост каждого контейнера"`
	Mode       string       `json:"mode"`
	Start      time.Time    `json:"start"`
	End        time.Time    `json:"end"`
	TotalLines int          `json:"total_lines" jsonschema:"строк получено за окно (после фильтров)"`
	Truncated  bool         `json:"truncated" jsonschema:"true — строк за окно больше лимита выборки, сузь окно или добавь pattern"`
	Patterns   []LogPattern `json:"patterns,omitempty"`
	Lines      []LogLine    `json:"lines,omitempty"`
	// Services — только при поиске без service
	Services []LogServiceHits `json:"services,omitempty" jsonschema:"поиск по всем сервисам: где и сколько строк нашлось, больше всего — первым"`
}

type LogServiceHits struct {
	Service   string    `json:"service,omitempty" jsonschema:"пусто — под не из каталога, смотри namespace"`
	Namespace string    `json:"namespace,omitempty"`
	Count     int       `json:"count"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

type LogPattern struct {
	Count     int       `json:"count"`
	Level     string    `json:"level,omitempty"`
	Template  string    `json:"template"`
	Example   string    `json:"example"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Workloads []string  `json:"workloads,omitempty" jsonschema:"workload'ы сервиса, из подов которых строки паттерна"`
	Services  []string  `json:"services,omitempty" jsonschema:"поиск по всем сервисам: сервисы, в логах которых паттерн"`
}

type LogLine struct {
	TS       time.Time `json:"ts"`
	Level    string    `json:"level,omitempty"`
	Workload string    `json:"workload,omitempty"`
	// Service, Namespace — только при поиске без service; namespace — если под не из каталога
	Service   string `json:"service,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Text      string `json:"text"`
}

const maxLineChars = 2000

func EncodeQueryLogsRep(v *usecaseLogsModel.QueryResult) QueryLogsRep {
	return QueryLogsRep{
		Service:    v.Service,
		Selector:   v.Selector,
		Source:     v.Source,
		Mode:       v.Mode,
		Start:      tz.In(v.Start),
		End:        tz.In(v.End),
		TotalLines: v.TotalLines,
		Truncated:  v.Truncated,
		Patterns:   lo.Map(v.Patterns, EncodeLogPattern),
		Lines: lo.Map(v.Lines, func(l logsModel.Line, _ int) LogLine {
			line := LogLine{TS: tz.In(l.TS), Level: l.Level, Workload: l.Workload, Service: l.Service, Text: lo.Ellipsis(l.Text, maxLineChars)}
			if l.Service == "" {
				line.Namespace = l.Namespace
			}
			return line
		}),
		Services: lo.Map(v.Services, func(h logsModel.ServiceHits, _ int) LogServiceHits {
			return LogServiceHits{Service: h.Service, Namespace: h.Namespace, Count: h.Count, FirstSeen: tz.In(h.FirstSeen), LastSeen: tz.In(h.LastSeen)}
		}),
	}
}

func EncodeLogPattern(v logsModel.Pattern, _ int) LogPattern {
	return LogPattern{
		Count:     v.Count,
		Level:     v.Level,
		Template:  lo.Ellipsis(v.Template, maxLineChars),
		Example:   lo.Ellipsis(v.Example, maxLineChars),
		FirstSeen: tz.In(v.FirstSeen),
		LastSeen:  tz.In(v.LastSeen),
		Workloads: v.Workloads,
		Services:  v.Services,
	}
}
