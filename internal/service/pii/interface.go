package pii

// Tokenizer — персональные данные токенами (docs/service-manifest.md, «Персональные данные —
// токенами»): агент видит pii:<вид>:<код>, а настоящее значение pulse подставляет только в свои
// исходящие запросы (Loki, ручки сервисов). Один и тот же телефон — один и тот же токен.
type Tokenizer interface {
	// Tokenize — токен значения вида kind; номер карты — только маска с последними цифрами.
	Tokenize(kind, value string) string
	// Text заменяет телефоны и email в тексте токенами, карты — маской.
	Text(s string) string
	// Resolve — значение для исходящего запроса: токен → значение (вид должен совпасть), иначе
	// само значение, если оно похоже на вид kind.
	Resolve(kind, value string) (string, error)
	// SearchPattern — поиск по токену в логах: literal — быстрый предфильтр (подстрока, может
	// быть пустым), regex — точная проверка; без токенов — ("", pattern).
	SearchPattern(pattern string) (literal, regex string, err error)
}
