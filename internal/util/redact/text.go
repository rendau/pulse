package redact

import (
	"regexp"
	"strings"
)

// PII в свободном тексте (строки логов): до ответа инструмента и, значит, до внешней LLM
// номера телефонов, email и номера карт не доходят. Маскирование грубое намеренно —
// лишняя маска в логе дешевле утёкшего номера.
var (
	emailRe = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	// карта: 13–19 цифр, допускаются пробелы и дефисы между группами; маскируется, только если
	// проходит проверку Луна — длинные идентификаторы заказов не трогаются
	cardRe = regexp.MustCompile(`\b\d(?:[ -]?\d){12,18}\b`)
	// телефон: международный с «+» (10–15 цифр) или 11 цифр с 7/8 в начале (KZ/RU),
	// с пробелами, дефисами и скобками между группами
	phoneRe = regexp.MustCompile(`\+\d(?:[ ()-]*\d){9,14}\b|\b[78](?:[ ()-]*\d){10}\b`)
	// значение явного поля phone/email/card — целиком, в каком бы формате ни было
	// (значение в кавычках — целиком, без кавычек — с группами цифр через пробел: «+7 701 123 45 67»)
	piiFieldRe = regexp.MustCompile(`(?i)("?(?:phone|tel|msisdn|email|e-mail|card|pan)(?:_?(?:number|num|no))?"?\s*[:=]\s*)("[^"]*"|[^",\s}]+(?:[ ()-]+\d+)*)`)
)

// виды персональных данных, которые находятся в свободном тексте
const (
	KindPhone = "phone"
	KindEmail = "email"
	KindCard  = "card"
)

// Text маскирует PII в строке: значение явного поля (phone=…) — ***, в тексте — <PHONE>,
// <EMAIL>, <CARD>.
func Text(s string) string {
	return ReplacePII(s, func(kind, _ string, field bool) string {
		if field {
			return Mask
		}
		return "<" + strings.ToUpper(kind) + ">"
	})
}

// ReplacePII заменяет персональные данные в тексте тем, что вернёт replace: kind — phone,
// email или card; value — найденное значение; field — значение явного поля (phone: …),
// а не находка в тексте. Правила поиска — одни для маскирования и для токенов.
func ReplacePII(s string, replace func(kind, value string, field bool) string) string {
	s = piiFieldRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := piiFieldRe.FindStringSubmatch(m)
		name := strings.ToLower(sub[1])
		kind := KindPhone
		switch {
		case strings.Contains(name, "mail"):
			kind = KindEmail
		case strings.Contains(name, "card"), strings.Contains(name, "pan"):
			kind = KindCard
		}
		if value, ok := strings.CutPrefix(sub[2], `"`); ok {
			return sub[1] + `"` + replace(kind, strings.TrimSuffix(value, `"`), true) + `"`
		}
		return sub[1] + replace(kind, sub[2], true)
	})
	s = emailRe.ReplaceAllStringFunc(s, func(m string) string { return replace(KindEmail, m, false) })
	s = cardRe.ReplaceAllStringFunc(s, func(m string) string {
		if luhn(digits(m)) {
			return replace(KindCard, m, false)
		}
		return m
	})
	return phoneRe.ReplaceAllStringFunc(s, func(m string) string { return replace(KindPhone, m, false) })
}

// Digits — только цифры строки.
func Digits(s string) string {
	return digits(s)
}

func digits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}

func luhn(number string) bool {
	sum, double := 0, false
	for i := len(number) - 1; i >= 0; i-- {
		d := int(number[i] - '0')
		if double {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return len(number) >= 13 && sum%10 == 0
}
