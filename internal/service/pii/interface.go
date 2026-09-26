package pii

// Cleaner — персональные данные в данных pulse (docs/service-manifest.md, «Персональные данные»).
// Токенов pulse не выдаёт: телефоны и email отдаются как есть, а прячет их от модели клиент
// pulse (агент) — по тексту и по отметкам personal_fields. pulse вырезает только то, что не
// нужно никому: учётные данные в адресах и номера карт.
type Cleaner interface {
	// Text — учётные данные в адресах вырезаны, карты — *** + последние 4 цифры.
	Text(s string) string
	// Value — значение поля вида kind из ответа ручки: карта — маской, остальное как есть.
	Value(kind, value string) string
	// Normalize — значение персонального параметра ручки, приведённое к одному виду
	// (телефон — цифры с кодом страны); не похоже на вид kind — ошибка.
	Normalize(kind, value string) (string, error)
	// SearchPattern — поиск в логах: телефон (+… целиком) — в любом написании, email — без
	// регистра; literal — быстрый предфильтр (подстрока, может быть пустым), regex — точная
	// проверка; остальное — ("", pattern).
	SearchPattern(pattern string) (literal, regex string)
}
