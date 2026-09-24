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

// translit — кириллица → латиница: основной вариант и альтернативы для букв, которые
// в именах сервисов пишут по-разному (к → k|c: «караван» → caravan, х → h|kh, ц → ts|c…;
// в заимствованных словах у → oo, и → ee: «лум» → loom, «фид» → feed).
// Казахские буквы — к ближайшей латинской.
var translit = map[rune][]string{
	'а': {"a"}, 'б': {"b"}, 'в': {"v", "w"}, 'г': {"g"}, 'д': {"d"}, 'е': {"e"}, 'ё': {"e", "yo"},
	'ж': {"zh", "j"}, 'з': {"z"}, 'и': {"i", "ee"}, 'й': {"y", "i"}, 'к': {"k", "c"}, 'л': {"l"},
	'м': {"m"}, 'н': {"n"}, 'о': {"o"}, 'п': {"p"}, 'р': {"r"}, 'с': {"s"}, 'т': {"t"},
	'у': {"u", "oo"}, 'ф': {"f"}, 'х': {"h", "kh"}, 'ц': {"ts", "c"}, 'ч': {"ch"}, 'ш': {"sh"},
	'щ': {"sch", "shch"}, 'ъ': {""}, 'ы': {"y", "i"}, 'ь': {""}, 'э': {"e"}, 'ю': {"yu", "u"},
	'я': {"ya", "ia"},
	'ә': {"a"}, 'ғ': {"g"}, 'қ': {"k", "q"}, 'ң': {"n"}, 'ө': {"o"}, 'ұ': {"u"}, 'ү': {"u"},
	'һ': {"h"}, 'і': {"i"},
}

// maxTranslitVariants — потолок вариантов на строку: альтернативы перебираются по буквам
// целиком (все «к» сразу), а не по позициям, поэтому вариантов 2^(число разных букв с
// альтернативами) — обычно единицы.
const maxTranslitVariants = 16

// Translit возвращает латинские варианты написания строки с кириллицей (основной — первым);
// без кириллицы — nil.
func Translit(s string) []string {
	s = strings.ToLower(s)

	var ambiguous []rune
	hasCyrillic := false
	for _, r := range s {
		if v, ok := translit[r]; ok {
			hasCyrillic = true
			if len(v) > 1 && runeIndex(ambiguous, r) < 0 {
				ambiguous = append(ambiguous, r)
			}
		}
	}
	if !hasCyrillic {
		return nil
	}

	variants := make([]string, 0, maxTranslitVariants)
	seen := map[string]struct{}{}
	for mask := 0; mask < 1<<len(ambiguous) && len(variants) < maxTranslitVariants; mask++ {
		var b strings.Builder
		for _, r := range s {
			v, ok := translit[r]
			if !ok {
				b.WriteRune(r)
				continue
			}
			alt := 0
			if i := runeIndex(ambiguous, r); i >= 0 && mask&(1<<i) != 0 {
				alt = 1
			}
			b.WriteString(v[alt])
		}
		if variant := b.String(); variant != "" {
			if _, ok := seen[variant]; !ok {
				seen[variant] = struct{}{}
				variants = append(variants, variant)
			}
		}
	}
	return variants
}

func runeIndex(runes []rune, r rune) int {
	for i, x := range runes {
		if x == r {
			return i
		}
	}
	return -1
}
