package redact

import (
	"regexp"
	"strings"
)

// secretNameRe — имя поля или параметра, похожее на секрет (docs/service-manifest.md,
// «Секреты объявлять нельзя»): такое поле в манифесте не принимается — переименуйте его.
var secretNameRe = regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|api_?key|apikey|private_?key|credential|dsn|cookie|session|authorization|signature)`)

// SecretName — имя похоже на секрет (password, token, api_key, dsn, cookie…).
func SecretName(name string) bool {
	return secretNameRe.MatchString(name)
}

// HasPII — в строке есть телефон, email или номер карты.
func HasPII(s string) bool {
	return Text(s) != s
}

// userinfoRe — учётные данные в адресе: scheme://user:pass@host.
var userinfoRe = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s]+@`)

// Userinfo вырезает учётные данные из адресов в тексте (postgres://user:pass@host → postgres://***@host).
func Userinfo(s string) string {
	if !strings.Contains(s, "@") {
		return s
	}
	return userinfoRe.ReplaceAllString(s, "${1}"+Mask+"@")
}
