// Package service — персональные данные в данных pulse: карты и учётные данные вырезаются,
// телефоны и email — как есть (от модели их прячет агент), приведение значений к одному виду
// и поиск телефона в логах в любом написании.
package service

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/rendau/pulse/internal/errs"
	"github.com/rendau/pulse/internal/util/redact"
)

// searchPhoneRe — шаблон поиска, который целиком телефон с «+» (так агент подставляет номер
// вместо токена): +77011234567, +7 701 123-45-67.
var searchPhoneRe = regexp.MustCompile(`^\+\d[\d ()-]*\d$`)

// searchEmailRe — шаблон поиска, который целиком email.
var searchEmailRe = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`)

type Config struct {
	// CountryCode — код страны для телефонов: 8… и 10 цифр приводятся к нему
	CountryCode string
}

type Service struct {
	conf Config
}

func New(conf Config) *Service {
	if conf.CountryCode == "" {
		conf.CountryCode = "7"
	}
	return &Service{conf: conf}
}

func (s *Service) Text(text string) string {
	return redact.ReplacePII(redact.Userinfo(text), func(kind, value string, _ bool) string {
		if kind == redact.KindCard {
			return maskCard(value)
		}
		return value
	})
}

func (s *Service) Value(kind, value string) string {
	if kind == redact.KindCard {
		return maskCard(value)
	}
	return s.Text(value)
}

func (s *Service) Normalize(kind, value string) (string, error) {
	normalized, ok := s.normalize(kind, strings.TrimSpace(value))
	if !ok {
		return "", fmt.Errorf("%w: value does not look like %s", errs.InvalidRequest, kind)
	}
	return normalized, nil
}

func (s *Service) SearchPattern(pattern string) (string, string) {
	switch {
	case searchPhoneRe.MatchString(pattern):
		d, ok := s.normalize("phone", pattern)
		if !ok {
			return "", pattern
		}
		// национальная часть слитно: в машинных логах номер почти всегда без пробелов
		// (+77011234567, 87011234567, 7011234567); с пробелами внутри предфильтр не найдёт
		national := strings.TrimPrefix(d, s.conf.CountryCode)
		return national, `(?:^|[^0-9])(?:\+?` + regexp.QuoteMeta(s.conf.CountryCode) + `|8)?` + national + `(?:[^0-9]|$)`
	case searchEmailRe.MatchString(pattern):
		// регистр в логах любой: подстрочного предфильтра нет
		return "", "(?i)" + regexp.QuoteMeta(pattern)
	default:
		return "", pattern
	}
}

// normalize — значение одного вида: сервис получает телефон одинаково, как бы его ни написали.
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
		v := strings.ToLower(value)
		return v, strings.Count(v, "@") == 1 && !strings.ContainsAny(v, " \t")
	case "iin":
		d := redact.Digits(value)
		return d, len(d) >= 6
	default:
		return value, value != ""
	}
}

func maskCard(value string) string {
	d := redact.Digits(value)
	if len(d) < 4 {
		return redact.Mask
	}
	return redact.Mask + d[len(d)-4:]
}
