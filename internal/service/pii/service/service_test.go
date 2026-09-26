package service

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/pulse/internal/errs"
)

func TestText(t *testing.T) {
	s := New(Config{})

	text := s.Text(`sms to +7 701 123 45 67 failed, email: ivan@mail.kz, card 4111 1111 1111 1111, dsn postgres://app:secret@db`)
	assert.Contains(t, text, "+7 701 123 45 67", "телефон как есть — прячет агент")
	assert.Contains(t, text, "ivan@mail.kz")
	assert.Contains(t, text, "***1111")
	assert.NotContains(t, text, "4111 1111")
	assert.NotContains(t, text, "secret")
	assert.Equal(t, text, s.Text(text), "повторная обработка ничего не меняет")

	assert.Equal(t, `{"card":"***1111"}`, s.Text(`{"card":"4111111111111111"}`), "значение явного поля")
	assert.Equal(t, "***1111", s.Value("card", "4111 1111 1111 1111"))
	assert.Equal(t, "8 701 123 45 67", s.Value("phone", "8 701 123 45 67"))
}

func TestNormalize(t *testing.T) {
	s := New(Config{})

	for _, v := range []string{"+7 (701) 123-45-67", "87011234567", "7011234567", "+77011234567"} {
		value, err := s.Normalize("phone", v)
		require.NoError(t, err, v)
		assert.Equal(t, "77011234567", value, v)
	}
	value, err := s.Normalize("email", " Ivan@Mail.KZ ")
	require.NoError(t, err)
	assert.Equal(t, "ivan@mail.kz", value)

	_, err = s.Normalize("phone", "Иванов")
	require.ErrorIs(t, err, errs.InvalidRequest)
	_, err = s.Normalize("phone", "pii:phone:abcdefghijkl")
	require.ErrorIs(t, err, errs.InvalidRequest, "токен pulse не раскрывает — это дело агента")
}

func TestSearchPattern(t *testing.T) {
	s := New(Config{})

	literal, pattern := s.SearchPattern("+7 701 123 45 67")
	assert.Equal(t, "7011234567", literal)
	re := regexp.MustCompile(pattern)
	for _, line := range []string{`phone=+77011234567`, `"phone":"87011234567"`, `to 7011234567 failed`} {
		assert.True(t, re.MatchString(line), line)
	}
	assert.False(t, re.MatchString(`order 170112345678`), "часть другого числа")

	literal, pattern = s.SearchPattern("Ivan@Mail.kz")
	assert.Empty(t, literal)
	assert.Regexp(t, pattern, "user ivan@mail.KZ")

	literal, pattern = s.SearchPattern("234115")
	assert.Empty(t, literal)
	assert.Equal(t, "234115", pattern, "не телефон и не email — как есть")
}
