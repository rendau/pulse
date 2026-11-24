package service

import (
	"regexp"
	"strings"

	"github.com/samber/lo"

	"github.com/rendau/pulse/internal/domain/edge/model"
)

// Signatures — признаки взлома в пути запроса. Список дополняется здесь: подстроки — для
// предфильтра в Loki (регистр — частые варианты), регэксп — точное условие (без учёта регистра).
var Signatures = []model.Signature{
	{Name: "path_traversal", Substrings: []string{".."}, Regex: `\.\.`},
	{Name: "encoded_dot", Substrings: []string{"%2e", "%2E"}, Regex: `(?i)%2e`},
	{Name: "encoded_slash", Substrings: []string{"%2f", "%2F", "%5c", "%5C"}, Regex: `(?i)%(?:2f|5c)`},
	{Name: "cloud_metadata", Substrings: []string{"169.254.169.254"}, Regex: `169\.254\.169\.254`},
	{Name: "dotenv", Substrings: []string{"/.env", "/.ENV"}, Regex: `(?i)/\.env`},
	{Name: "git", Substrings: []string{"/.git", "/.GIT"}, Regex: `(?i)/\.git`},
	{Name: "wordpress", Substrings: []string{"wp-", "WP-"}, Regex: `(?i)wp-`},
	{Name: "api_docs", Substrings: []string{"/doc", "/Doc", "/DOC", "/swagger", "/Swagger", "/openapi", "/api-docs"},
		Regex: `(?i)/(?:docs?|swagger|openapi|api-docs)(?:[/._-]|$)`},
	{Name: "actuator", Substrings: []string{"/actuator", "/Actuator"}, Regex: `(?i)/actuator`},
	{Name: "admin", Substrings: []string{"/admin", "/Admin", "/ADMIN"}, Regex: `(?i)/admin`},
}

var signatureRes = lo.Map(Signatures, func(s model.Signature, _ int) *regexp.Regexp {
	return regexp.MustCompile(s.Regex)
})

// MatchSignatures — имена признаков взлома, найденных в пути (по порядку списка).
func MatchSignatures(path string) []string {
	return lo.FilterMap(Signatures, func(s model.Signature, i int) (string, bool) {
		return s.Name, signatureRes[i].MatchString(path)
	})
}

// SignaturesRegex — все признаки одним регэкспом без учёта регистра (для фильтра в Loki).
// Первая группа — совпавший кусок пути: по нему MatchSignatures называет признак.
func SignaturesRegex() string {
	return `(?i)(` + strings.Join(lo.Map(Signatures, func(s model.Signature, _ int) string {
		return strings.TrimPrefix(s.Regex, "(?i)")
	}), "|") + `)`
}

// SignaturesSubstrings — подстроки всех признаков для предфильтра строк.
func SignaturesSubstrings() []string {
	return lo.Uniq(lo.FlatMap(Signatures, func(s model.Signature, _ int) []string { return s.Substrings }))
}
