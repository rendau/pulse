// Package fuzzy — нечёткое сравнение строк для резолвинга имён сервисов.
package fuzzy

import (
	"strings"
	"unicode"
)

// Normalize приводит строку к виду для сравнения: нижний регистр, обрезка,
// подчёркивания и пробелы → дефис (kafka_producer ≡ kafka-producer).
func Normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.Map(func(r rune) rune {
		if r == '_' || unicode.IsSpace(r) {
			return '-'
		}
		return r
	}, s)
}

// Tokens разбивает запрос на слова (буквы/цифры), отбрасывая короче minLen.
func Tokens(s string, minLen int) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	result := fields[:0]
	for _, f := range fields {
		if len([]rune(f)) >= minLen {
			result = append(result, f)
		}
	}
	return result
}

// Similarity — 1 - levenshtein/maxLen, в диапазоне [0, 1].
func Similarity(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	maxLen := max(len(ra), len(rb))
	if maxLen == 0 {
		return 1
	}
	return 1 - float64(levenshtein(ra, rb))/float64(maxLen)
}

func levenshtein(a, b []rune) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}

	return prev[len(b)]
}
