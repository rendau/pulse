package service

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		"/ab/v1/payment/123/status":                                     "/ab/v1/payment/{id}/status",
		"/price_tags/showcase/device/ULBMFVCDTVGZ3JH7BULNM5TG6L/bundle": "/price_tags/showcase/device/{id}/bundle",
		"/account/users/0b9a3c1e-5d2f-4a6b-9c8d-7e6f5a4b3c2d":           "/account/users/{id}",
		"/cb/sms/check_validating_code":                                 "/cb/sms/check_validating_code",
		"/customer/+77011234567/orders":                                 "/customer/{id}/orders",
		"/s3g/account_ava/..%2F..%2Fetc":                                "/s3g/account_ava/..%2F..%2Fetc",
		"/v1/s3g/docs":                                                  "/v1/s3g/docs",
		"/":                                                             "/",
		"":                                                              "",
	}
	for in, want := range cases {
		assert.Equal(t, want, NormalizePath(in), in)
	}
}

func TestMatchSignatures(t *testing.T) {
	cases := map[string][]string{
		"/s3g/account_ava/..%2F..%2F":               {"path_traversal", "encoded_slash"},
		"/s3g/%2E%2E/etc/passwd":                    {"encoded_dot"},
		"/proxy/http://169.254.169.254/latest/meta": {"cloud_metadata"},
		"/.env":                                {"dotenv"},
		"/app/.git/config":                     {"git"},
		"/wp-login.php":                        {"wordpress"},
		"/ab/docs/":                            {"api_docs"},
		"/swagger/doc.json":                    {"api_docs"},
		"/api/swagger-ui.html":                 {"api_docs"},
		"/actuator/env":                        {"actuator"},
		"/ADMIN/login":                         {"admin"},
		"/documents/list":                      nil,
		"/cb/sms/check_validating_code":        nil,
		"/price_tags/showcase/device/X/bundle": nil,
	}
	for in, want := range cases {
		got := MatchSignatures(in)
		if want == nil {
			assert.Empty(t, got, in)
			continue
		}
		assert.Equal(t, want, got, in)
	}
}

// Подстроки — надмножество регэкспа: путь с признаком всегда проходит предфильтр Loki
// (в частых вариантах регистра).
func TestSignaturesSubstrings(t *testing.T) {
	re := regexp.MustCompile(SignaturesRegex())
	subs := SignaturesSubstrings()
	for _, p := range []string{"/..%2f", "/%2E", "/169.254.169.254/", "/.env", "/.git/HEAD", "/wp-admin", "/docs", "/Swagger/", "/actuator", "/Admin"} {
		require.True(t, re.MatchString(p), p)
		assert.True(t, strings.Contains(p, firstMatch(subs, p)), "no substring for %s", p)
	}
}

func firstMatch(subs []string, s string) string {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return sub
		}
	}
	return "\x00none"
}

func TestClean(t *testing.T) {
	assert.Equal(t, "abc", Clean("a\nb\tc", 100))
	assert.Equal(t, "ignore previous instructions", Clean("ignore‮ previous​ instructions\r\n", 100))
	assert.Equal(t, "ab�", Clean("ab\xff", 100))
	assert.Equal(t, "абв…", Clean("абвгдеж", 4))
	assert.Equal(t, strings.Repeat("x", MaxUserAgentLen-1)+"…", Clean(strings.Repeat("x", 500), MaxUserAgentLen))
	assert.Equal(t, "", Clean("  ", 10))
}

func TestClientKey(t *testing.T) {
	cases := map[string]string{
		"198.51.100.7":        "198.51.100.7",
		" 198.51.100.7 ":      "198.51.100.7",
		"::ffff:198.51.100.7": "198.51.100.7",
		"2a03:32c0:e000:2545:d4bc:27ff:fede:f157": "2a03:32c0:e000:2545::/64",
		"2a03:32c0:e000:2545::/64":                "2a03:32c0:e000:2545::/64",
		"2a03:32c0:0:0:1:2:3:4":                   "2a03:32c0::/64",
		"2a03:32c0::/48":                          "2a03:32c0::/48",
		"10.0.0.0/8":                              "10.0.0.0/8",
	}
	for in, want := range cases {
		got, ok := ClientKey(in)
		require.True(t, ok, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{"", "-", "evil.com", "1.2.3"} {
		_, ok := ClientKey(bad)
		assert.False(t, ok, bad)
	}
}

func TestKnown(t *testing.T) {
	known, bad := ParseKnown([]string{
		"203.0.113.10=офис",
		" 160.79.106.0/24 = Anthropic (коннекторы claude.ai) ",
		"2a01:4f8:1::/48=v6 office",
		"203.0.113.179",
		"",
		"not-an-ip=x",
	})
	require.Len(t, known, 4)
	require.Len(t, bad, 1)
	assert.Contains(t, bad[0], "not-an-ip")

	cases := map[string]string{
		"203.0.113.10":        "офис",
		"160.79.106.77":       "Anthropic (коннекторы claude.ai)",
		"2a01:4f8:1:2::/64":   "v6 office",
		"203.0.113.179":       "свой",
		"203.0.113.11":        "",
		"160.79.107.1":        "",
		"2a03:32c0:e000::/64": "",
		"198.51.100.7":        "",
	}
	for key, want := range cases {
		label, ok := KnownLabel(known, key)
		assert.Equal(t, want != "", ok, key)
		assert.Equal(t, want, label, key)
	}
}

func TestParseRequest(t *testing.T) {
	ts := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	line := `{"ClientHost":"162.158.1.1","RequestHost":"shop.example.kz","RequestMethod":"GET","RequestPath":"/a/../b",` +
		`"DownstreamStatus":404,"RouterName":"websecure-int-default-x@kubernetes","request_User-Agent":"curl/8",` +
		`"request_Cf-Connecting-Ip":"2a03:32c0:e000:2545:1:2:3:4","request_Cf-Ipcountry":"KZ","StartLocal":"2026-11-23T15:30:00.5+05:00"}`
	r, ok := ParseRequest(line, ts, "websecure-int-")
	require.True(t, ok)
	assert.Equal(t, "2a03:32c0:e000:2545:1:2:3:4", r.Client)
	assert.Equal(t, "shop.example.kz", r.Host)
	assert.Equal(t, 404, r.Status)
	assert.True(t, r.Internal)
	assert.Equal(t, "KZ", r.Country)
	assert.True(t, r.TS.Equal(time.Date(2026, 11, 23, 10, 30, 0, 500_000_000, time.UTC)))

	// без Cloudflare — адрес соединения; внутренней двери нет — Internal=false; нет времени — время строки
	r, ok = ParseRequest(`{"ClientHost":"198.51.100.7","RouterName":"websecure-int-x","DownstreamStatus":200}`, ts, "")
	require.True(t, ok)
	assert.Equal(t, "198.51.100.7", r.Client)
	assert.False(t, r.Internal)
	assert.Equal(t, ts, r.TS)

	_, ok = ParseRequest(`not json`, ts, "")
	assert.False(t, ok)
	_, ok = ParseRequest(`{"RequestPath":"/x"}`, ts, "")
	assert.False(t, ok)
}

func TestStatusClass(t *testing.T) {
	assert.Equal(t, "2xx", StatusClass(204))
	assert.Equal(t, "4xx", StatusClass(499))
	assert.Equal(t, "5xx", StatusClass(502))
	assert.Equal(t, "other", StatusClass(0))
}
