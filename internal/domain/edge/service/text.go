package service

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Пределы длины текста из запросов: путь, User-Agent, Referer приходят от клиента (в том числе
// от атакующего) и уходят модели — длинный текст тратит контекст и может нести «инструкции».
const (
	MaxPathLen      = 200
	MaxUserAgentLen = 150
	MaxRefererLen   = 150
)

// Clean — недоверенный текст из запроса в виде, пригодном для ответа: битый UTF-8 заменён,
// перевод строки и табуляция — пробел, прочие управляющие и невидимые символы (смена
// направления текста, нулевой ширины) убраны, длина — не больше max символов (обрезанное — с «…»).
func Clean(s string, maxLen int) string {
	s = strings.ToValidUTF8(s, "�")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return ' '
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if maxLen > 0 && utf8.RuneCountInString(s) > maxLen {
		s = string([]rune(s)[:maxLen-1]) + "…"
	}
	return s
}

// CleanLines — Clean для многострочного текста: строки чистятся по отдельности, переводы
// строк остаются (не больше одной пустой строки подряд), длина — не больше max символов.
func CleanLines(s string, maxLen int) string {
	var lines []string
	blank := 0
	for line := range strings.Lines(strings.ToValidUTF8(s, "�")) {
		line = Clean(line, 0)
		if line == "" {
			if blank++; blank > 1 || len(lines) == 0 {
				continue
			}
		} else {
			blank = 0
		}
		lines = append(lines, line)
	}
	s = strings.TrimSpace(strings.Join(lines, "\n"))
	if maxLen > 0 && utf8.RuneCountInString(s) > maxLen {
		s = strings.TrimSpace(string([]rune(s)[:maxLen-1])) + "…"
	}
	return s
}

var (
	digitsSegRe = regexp.MustCompile(`^\+?\d+$`)
	uuidSegRe   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	// длинный токен из букв и цифр (код устройства, хэш, base32/64-идентификатор)
	tokenSegRe = regexp.MustCompile(`^[A-Za-z0-9_-]{16,}$`)
	hasDigitRe = regexp.MustCompile(`\d`)
)

// NormalizePath склеивает изменчивые сегменты пути в {id}: числа (и телефоны), uuid, длинные
// токены из букв и цифр. /ab/v1/payment/123/status и /ab/v1/payment/456/status — один путь.
// Признаки взлома (.., %2f) не трогаются: сегмент с ними не похож на идентификатор.
func NormalizePath(p string) string {
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		if isIdSegment(seg) {
			segs[i] = "{id}"
		}
	}
	return strings.Join(segs, "/")
}

func isIdSegment(seg string) bool {
	switch {
	case seg == "":
		return false
	case digitsSegRe.MatchString(seg), uuidSegRe.MatchString(seg):
		return true
	default:
		return tokenSegRe.MatchString(seg) && hasDigitRe.MatchString(seg)
	}
}
