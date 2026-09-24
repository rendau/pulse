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
	piiFieldRe = regexp.MustCompile(`(?i)("?(?:phone|tel|msisdn|email|e-mail|card|pan)(?:_?(?:number|num|no))?"?\s*[:=]\s*"?)([^",\s}]+)`)
)

// Text маскирует PII в строке: <PHONE>, <EMAIL>, <CARD>.
func Text(s string) string {
	s = piiFieldRe.ReplaceAllString(s, "${1}"+Mask)
	s = emailRe.ReplaceAllString(s, "<EMAIL>")
	s = cardRe.ReplaceAllStringFunc(s, func(m string) string {
		if luhn(digits(m)) {
			return "<CARD>"
		}
		return m
	})
	return phoneRe.ReplaceAllString(s, "<PHONE>")
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
