package service

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse/internal/errs"
)

func TestTokenize(t *testing.T) {
	s := New(Config{Key: []byte("k")})

	token := s.Tokenize("phone", "+7 (701) 123-45-67")
	assert.Regexp(t, `^pii:phone:[a-p]{12}$`, token)
	assert.Equal(t, token, s.Tokenize("phone", "87011234567"), "одно значение в разном написании — один токен")
	assert.Equal(t, token, s.Tokenize("phone", "7011234567"))
	assert.NotEqual(t, token, s.Tokenize("phone", "+7 701 123 45 68"))
	assert.Equal(t, token, s.Tokenize("phone", token), "токен не токенизируется повторно")

	assert.Equal(t, s.Tokenize("email", "Ivan@Mail.KZ"), s.Tokenize("email", "ivan@mail.kz"))
	assert.Equal(t, "***1111", s.Tokenize("card", "4111 1111 1111 1111"), "карта — только маска")
	assert.Equal(t, "***", s.Tokenize("phone", "12"), "не телефон")

	other := New(Config{Key: []byte("другой ключ")})
	assert.NotEqual(t, token, other.Tokenize("phone", "87011234567"), "без ключа не подобрать")
}

func TestText(t *testing.T) {
	s := New(Config{Key: []byte("k")})
	phone := s.Tokenize("phone", "87011234567")

	text := s.Text(`sms to +7 701 123 45 67 failed, email: ivan@mail.kz, card 4111 1111 1111 1111, dsn postgres://app:secret@db`)
	assert.Contains(t, text, phone)
	assert.Contains(t, text, s.Tokenize("email", "ivan@mail.kz"))
	assert.Contains(t, text, "***1111")
	assert.NotContains(t, text, "secret")
	assert.Equal(t, text, s.Text(text), "повторная обработка ничего не меняет")

	assert.Contains(t, s.Text(`{"phone":"87011234567"}`), phone, "значение явного поля")
}

func TestResolveAndSearch(t *testing.T) {
	s := New(Config{Key: []byte("k")})
	phone := s.Tokenize("phone", "+7 701 123 45 67")
	name := s.Tokenize("name", "Иван Петров")

	value, err := s.Resolve("phone", phone)
	require.NoError(t, err)
	assert.Equal(t, "77011234567", value)

	value, err = s.Resolve("phone", "8 701 123 45 67")
	require.NoError(t, err)
	assert.Equal(t, "77011234567", value, "сырое значение от человека — тоже, приведённое")

	_, err = s.Resolve("email", phone)
	require.ErrorIs(t, err, errs.InvalidRequest, "вид токена не совпал")
	_, err = s.Resolve("phone", "Иванов")
	require.ErrorIs(t, err, errs.InvalidRequest)
	_, err = s.Resolve("phone", "pii:phone:abcdefghijkl")
	require.ErrorIs(t, err, errs.InvalidRequest, "неизвестный токен")

	literal, pattern, err := s.SearchPattern(phone)
	require.NoError(t, err)
	assert.Equal(t, "7011234567", literal, "предфильтр — национальная часть слитно")
	re := regexp.MustCompile(pattern)
	for _, line := range []string{"to +77011234567", "to 87011234567", "to 7011234567", `phone=77011234567`, `"phone":"77011234567"`} {
		assert.True(t, re.MatchString(line) && strings.Contains(line, literal), line)
	}
	assert.False(t, re.MatchString("to 87011234568"))
	assert.False(t, re.MatchString("id 177011234567"), "не внутри другого числа")

	literal, pattern, err = s.SearchPattern("заказ 234115")
	require.NoError(t, err)
	assert.Empty(t, literal)
	assert.Equal(t, "заказ 234115", pattern, "без токенов — как есть")

	_, _, err = s.SearchPattern(name)
	require.ErrorIs(t, err, errs.InvalidRequest, "по имени искать нельзя")
	_, _, err = s.SearchPattern("ошибка " + phone)
	require.ErrorIs(t, err, errs.InvalidRequest, "токен — только целиком")
}

func TestVaultLimits(t *testing.T) {
	s := New(Config{Key: []byte("k"), Ttl: time.Hour, MaxEntries: 3})
	var tokens []string
	for i := range 5 {
		tokens = append(tokens, s.Tokenize("iin", strings.Repeat("1", 6)+string(rune('0'+i))))
	}
	assert.Len(t, s.vault, 3)
	_, err := s.Resolve("iin", tokens[4])
	require.NoError(t, err, "свежие — на месте")

	s.mu.Lock()
	e := s.vault[tokens[4]]
	e.at = time.Now().Add(-2 * time.Hour)
	s.vault[tokens[4]] = e
	s.mu.Unlock()
	_, err = s.Resolve("iin", tokens[4])
	require.ErrorIs(t, err, errs.InvalidRequest, "устаревший")
}
