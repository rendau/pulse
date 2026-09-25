// Package service — токены персональных данных: HMAC значения, приведённого к одному виду
// (телефон: цифры с кодом страны), и память «токен → значение» для обратной подстановки.
package service

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mechta-market/pulse/internal/errs"
	"github.com/mechta-market/pulse/internal/util/redact"
)

const (
	// tokenChars — длина кода токена: 12 букв a–p (48 бит). Букв, а не цифр: токен не должен
	// похоже на телефон или номер карты при повторной обработке текста
	tokenChars = 12
	prefix     = "pii:"
)

// виды, по токену которых можно искать дальше (обратная подстановка)
var searchable = []string{"phone", "email", "iin", "customer_id"}

// TokenRe — токен в тексте.
var TokenRe = regexp.MustCompile(`pii:(phone|email|iin|customer_id|name|address|document|other):[a-p]{12}`)

type Config struct {
	// Key — ключ HMAC (секрет); пусто — случайный на время жизни процесса (токены не
	// совпадут после рестарта)
	Key []byte
	// CountryCode — код страны для телефонов: 8… и 10 цифр приводятся к нему
	CountryCode string
	// Ttl — сколько помнить значение токена; MaxEntries — потолок памяти
	Ttl        time.Duration
	MaxEntries int
}

type Service struct {
	conf Config

	mu    sync.Mutex
	vault map[string]entry
}

type entry struct {
	kind  string
	value string
	at    time.Time
}

func New(conf Config) *Service {
	if len(conf.Key) == 0 {
		conf.Key = make([]byte, 32)
		_, _ = rand.Read(conf.Key)
		slog.Warn("PII_TOKEN_KEY is empty: personal data tokens are random per process and change after restart")
	}
	if conf.CountryCode == "" {
		conf.CountryCode = "7"
	}
	if conf.Ttl <= 0 {
		conf.Ttl = 24 * time.Hour
	}
	if conf.MaxEntries <= 0 {
		conf.MaxEntries = 100_000
	}
	return &Service{conf: conf, vault: map[string]entry{}}
}

func (s *Service) Tokenize(kind, value string) string {
	value = strings.TrimSpace(value)
	if value == "" || TokenRe.MatchString(value) {
		return value
	}
	if kind == redact.KindCard {
		return maskCard(value)
	}
	normalized, ok := s.normalize(kind, value)
	if !ok {
		return redact.Mask
	}

	mac := hmac.New(sha256.New, s.conf.Key)
	mac.Write([]byte(kind + ":" + normalized))
	sum := mac.Sum(nil)
	code := make([]byte, tokenChars)
	for i := range code {
		code[i] = 'a' + (sum[i/2]>>(4*(1-i%2)))&0x0f
	}
	token := prefix + kind + ":" + string(code)

	// по токенам без поиска значение не хранится: его не понадобится подставлять
	if slices.Contains(searchable, kind) {
		s.remember(token, kind, normalized)
	}
	return token
}

func (s *Service) Text(text string) string {
	// готовые токены не трогаются: иначе правило явного поля (phone: …) найдёт «phone:»
	// внутри самого токена
	var b strings.Builder
	last := 0
	for _, loc := range TokenRe.FindAllStringIndex(text, -1) {
		b.WriteString(s.replace(text[last:loc[0]]))
		b.WriteString(text[loc[0]:loc[1]])
		last = loc[1]
	}
	b.WriteString(s.replace(text[last:]))
	return b.String()
}

func (s *Service) replace(text string) string {
	return redact.ReplacePII(redact.Userinfo(text), func(kind, value string, _ bool) string {
		return s.Tokenize(kind, value)
	})
}

func (s *Service) Resolve(kind, value string) (string, error) {
	value = strings.TrimSpace(value)
	if TokenRe.MatchString(value) && TokenRe.FindString(value) == value {
		e, err := s.lookup(value)
		if err != nil {
			return "", err
		}
		if e.kind != kind {
			return "", fmt.Errorf("%w: token %s is %s, parameter expects %s", errs.InvalidRequest, value, e.kind, kind)
		}
		return e.value, nil
	}
	normalized, ok := s.normalize(kind, value)
	if !ok {
		return "", fmt.Errorf("%w: value does not look like %s; pass a pii:%s:… token from pulse responses", errs.InvalidRequest, kind, kind)
	}
	return normalized, nil
}

// SearchPattern — поиск по токену в логах: literal — подстрока, которую Loki проверяет быстро
// (предфильтр; пусто — без него), regex — точная проверка отобранных строк. Без токенов —
// ("", pattern): шаблон как есть.
func (s *Service) SearchPattern(pattern string) (string, string, error) {
	tokens := TokenRe.FindAllString(pattern, -1)
	if len(tokens) == 0 {
		return "", pattern, nil
	}
	if TokenRe.FindString(pattern) != pattern {
		return "", "", fmt.Errorf("%w: pass a pii token alone as pattern, without other text", errs.InvalidRequest)
	}
	e, err := s.lookup(pattern)
	if err != nil {
		return "", "", err
	}
	switch e.kind {
	case "phone":
		// национальная часть слитно: в машинных логах номер почти всегда без пробелов
		// (+77011234567, 87011234567, 7011234567); с пробелами внутри предфильтр не найдёт
		national := strings.TrimPrefix(e.value, s.conf.CountryCode)
		return national, `(?:^|[^0-9])(?:\+?` + regexp.QuoteMeta(s.conf.CountryCode) + `|8)?` + national + `(?:[^0-9]|$)`, nil
	case "email":
		// регистр в логах любой: подстрочного предфильтра нет
		return "", "(?i)" + regexp.QuoteMeta(e.value), nil
	default:
		return e.value, `(?:^|[^[:alnum:]])` + regexp.QuoteMeta(e.value) + `(?:[^[:alnum:]]|$)`, nil
	}
}

func (s *Service) lookup(token string) (entry, error) {
	kind := strings.Split(token, ":")[1]
	if !slices.Contains(searchable, kind) {
		return entry{}, fmt.Errorf("%w: %s token cannot be searched by (only %s)", errs.InvalidRequest, kind, strings.Join(searchable, ", "))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.vault[token]
	if !ok || time.Since(e.at) > s.conf.Ttl {
		return entry{}, fmt.Errorf("%w: token %s is unknown or expired — query the source (logs, endpoint) again to get a fresh token", errs.InvalidRequest, token)
	}
	return e, nil
}

func (s *Service) remember(token, kind, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vault[token] = entry{kind: kind, value: value, at: time.Now()}
	if len(s.vault) <= s.conf.MaxEntries {
		return
	}
	// переполнение: сначала устаревшие, затем самые старые
	for t, e := range s.vault {
		if time.Since(e.at) > s.conf.Ttl {
			delete(s.vault, t)
		}
	}
	for len(s.vault) > s.conf.MaxEntries {
		oldest, at := "", time.Now()
		for t, e := range s.vault {
			if e.at.Before(at) {
				oldest, at = t, e.at
			}
		}
		delete(s.vault, oldest)
	}
}

// normalize — значение одного вида: иначе +7 701…, 8701… и 7701… дали бы разные токены.
func (s *Service) normalize(kind, value string) (string, bool) {
	switch kind {
	case "phone":
		d := redact.Digits(value)
		switch {
		case len(d) == 10:
			d = s.conf.CountryCode + d
		case len(d) == 11 && d[0] == '8' && s.conf.CountryCode == "7":
			d = "7" + d[1:]
		}
		return d, len(d) >= 10 && len(d) <= 15
	case "email":
		v := strings.ToLower(strings.TrimSpace(value))
		return v, strings.Count(v, "@") == 1 && !strings.ContainsAny(v, " \t")
	case "iin":
		d := redact.Digits(value)
		return d, len(d) >= 6
	case "name":
		return strings.Join(strings.Fields(strings.ToLower(value)), " "), strings.TrimSpace(value) != ""
	default:
		return strings.TrimSpace(value), strings.TrimSpace(value) != ""
	}
}

func maskCard(value string) string {
	d := redact.Digits(value)
	if len(d) < 4 {
		return redact.Mask
	}
	return redact.Mask + d[len(d)-4:]
}
