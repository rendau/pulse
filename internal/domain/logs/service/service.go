// Package service — агрегация логов в паттерны: нормализация строки (маскирование чисел,
// UUID, IP, времени), группировка по шаблону и top-N по частоте. Чистые функции без
// обращения к источникам, чтобы использоваться и в query_logs, и в снапшоте (top_errors).
package service

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/samber/lo"

	"github.com/mechta-market/pulse/internal/domain/logs/model"
)

type Service struct{}

func New() *Service {
	return &Service{}
}

// маски применяются по порядку: от специфичных (uuid, время) к общим (числа)
var masks = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`), "<UUID>"},
	{regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`), "<TS>"},
	{regexp.MustCompile(`\d{2}:\d{2}:\d{2}(?:\.\d+)?`), "<TIME>"},
	{regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d+)?\b`), "<IP>"},
	{regexp.MustCompile(`\b[0-9a-fA-F]{16,}\b`), "<HEX>"},
	{regexp.MustCompile(`\b\d+(?:\.\d+)?(?:ms|µs|us|ns|s|m|h|KB|MB|GB|B)?\b`), "<NUM>"},
}

var (
	levelKeyRe  = regexp.MustCompile(`(?i)(?:"level"\s*:\s*"|\blevel=|\blvl=|"severity"\s*:\s*")(\w+)`)
	levelWordRe = regexp.MustCompile(`(?i)\b(ERROR|ERR|FATAL|PANIC|CRITICAL|WARN|WARNING|INFO|DEBUG|TRACE)\b`)
	spaceRe     = regexp.MustCompile(`\s+`)
)

// Normalize приводит строку к шаблону: маскирует изменчивые части.
func (s *Service) Normalize(line string) string {
	result := strings.TrimSpace(line)
	for _, m := range masks {
		result = m.re.ReplaceAllString(result, m.repl)
	}
	return spaceRe.ReplaceAllString(result, " ")
}

// DetectLevel определяет уровень строки: JSON-поле level/severity, key=value, иначе слово.
func (s *Service) DetectLevel(line string) string {
	if m := levelKeyRe.FindStringSubmatch(line); m != nil {
		return canonicalLevel(m[1])
	}
	if m := levelWordRe.FindStringSubmatch(line); m != nil {
		return canonicalLevel(m[1])
	}
	return ""
}

func canonicalLevel(s string) string {
	switch strings.ToLower(s) {
	case "error", "err", "fatal", "panic", "critical", "crit":
		return model.LevelError
	case "warn", "warning":
		return model.LevelWarn
	case "info":
		return model.LevelInfo
	case "debug", "trace":
		return model.LevelDebug
	default:
		return ""
	}
}

// MessageOf извлекает сообщение из структурированной строки, иначе возвращает строку целиком:
// шаблон должен строиться по сообщению, а не по всей строке с временем и трассировкой.
// Понимает JSON (msg/message + error/err), обёртку сборщика логов {"log": "…"} (fluent-bit,
// docker) и logfmt (level=ERROR msg="…" error="…", текстовый slog).
func (s *Service) MessageOf(line string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "{") {
		return logfmtMessage(trimmed)
	}

	var fields map[string]any
	if err := json.Unmarshal([]byte(trimmed), &fields); err != nil {
		return trimmed
	}

	// обёртка сборщика: сообщение приложения — внутри log
	if inner, ok := fields["log"].(string); ok && fields["msg"] == nil && fields["message"] == nil {
		return s.MessageOf(inner)
	}

	parts := make([]string, 0, 2)
	for _, key := range []string{"msg", "message"} {
		if v, ok := fields[key].(string); ok && v != "" {
			parts = append(parts, v)
			break
		}
	}
	for _, key := range []string{"error", "err"} {
		if v, ok := fields[key].(string); ok && v != "" {
			parts = append(parts, v)
			break
		}
	}
	if len(parts) == 0 {
		return trimmed
	}
	return strings.Join(parts, ": ")
}

// Aggregate группирует строки по шаблону и возвращает top-N по частоте.
func (s *Service) Aggregate(lines []model.Line, top int) []model.Pattern {
	groups := make(map[string]*model.Pattern, 64)
	workloads := make(map[string]map[string]struct{}, 64)

	for _, line := range lines {
		message := s.MessageOf(line.Text)
		key := line.Level + "\x00" + s.Normalize(message)

		p, ok := groups[key]
		if !ok {
			p = &model.Pattern{
				Level:     line.Level,
				Template:  s.Normalize(message),
				Example:   message,
				FirstSeen: line.TS,
				LastSeen:  line.TS,
			}
			groups[key] = p
			workloads[key] = map[string]struct{}{}
		}
		p.Count++
		if line.Workload != "" {
			workloads[key][line.Workload] = struct{}{}
		}
		if line.TS.Before(p.FirstSeen) {
			p.FirstSeen = line.TS
		}
		if line.TS.After(p.LastSeen) {
			p.LastSeen = line.TS
		}
	}

	patterns := lo.MapToSlice(groups, func(key string, p *model.Pattern) model.Pattern {
		p.Workloads = lo.Keys(workloads[key])
		sort.Strings(p.Workloads)
		return *p
	})
	sort.SliceStable(patterns, func(i, j int) bool {
		if patterns[i].Count != patterns[j].Count {
			return patterns[i].Count > patterns[j].Count
		}
		return patterns[i].Template < patterns[j].Template
	})

	if top > 0 && len(patterns) > top {
		patterns = patterns[:top]
	}

	return patterns
}

// logfmtKeyRe — ключ logfmt со значением в кавычках или без.
var logfmtKeyRe = regexp.MustCompile(`(?:^|\s)(msg|message|error|err)=("(?:[^"\\]|\\.)*"|\S+)`)

// logfmtMessage — msg и error из строки logfmt; не logfmt — строка как есть.
func logfmtMessage(line string) string {
	var msg, errText string
	for _, m := range logfmtKeyRe.FindAllStringSubmatch(line, -1) {
		value := m[2]
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}
		switch m[1] {
		case "msg", "message":
			if msg == "" {
				msg = value
			}
		default:
			if errText == "" {
				errText = value
			}
		}
	}
	switch {
	case msg == "":
		return line
	case errText == "":
		return msg
	default:
		return msg + ": " + errText
	}
}
