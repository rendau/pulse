package service

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"

	"github.com/rendau/pulse/internal/domain/oauth/model"
)

const (
	// AccessTokenPrefix отличает access-токены OAuth от статических токенов MCP.
	AccessTokenPrefix  = "pso_"
	RefreshTokenPrefix = "psr_"
	CodePrefix         = "psc_"

	tokenRandomBytes = 32

	pkceVerifierMinLen = 43
	pkceVerifierMaxLen = 128
)

var tokenPrefixes = map[string]string{
	model.TokenKindCode:    CodePrefix,
	model.TokenKindAccess:  AccessTokenPrefix,
	model.TokenKindRefresh: RefreshTokenPrefix,
}

// GenerateToken — новый токен вида kind и его хэш для хранения.
func GenerateToken(kind string) (token, hash string, err error) {
	raw := make([]byte, tokenRandomBytes)
	if _, err = rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("rand.Read: %w", err)
	}

	token = tokenPrefixes[kind] + hex.EncodeToString(raw)

	return token, HashToken(token), nil
}

// HashToken: токены — случайные строки высокой энтропии, sha256 без соли достаточно. Тем же
// хэшем запоминается ключ доступа подключения.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// VerifyPKCE сверяет code_verifier с code_challenge S256 (RFC 7636).
func VerifyPKCE(challenge, verifier string) bool {
	if len(verifier) < pkceVerifierMinLen || len(verifier) > pkceVerifierMaxLen {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	expected := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(expected), []byte(challenge)) == 1
}

// RedirectAllowed — подходит ли адрес возврата под один из разрешённых. Разрешённый адрес без
// пути пускает любой путь своего origin; loopback (localhost, 127.0.0.1, [::1]) — любой порт:
// нативные приложения вроде Claude Code слушают случайный порт (RFC 8252, 7.3).
func RedirectAllowed(allowed []string, uri string) bool {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme == "" || u.Host == "" || u.Fragment != "" || u.User != nil {
		return false
	}

	for _, item := range allowed {
		a, err := url.Parse(item)
		if err != nil {
			continue
		}
		if !strings.EqualFold(a.Scheme, u.Scheme) || !strings.EqualFold(a.Hostname(), u.Hostname()) {
			continue
		}
		if !isLoopback(a.Hostname()) && a.Port() != u.Port() {
			continue
		}
		if path := strings.TrimSuffix(a.Path, "/"); path != "" && path != u.Path {
			continue
		}
		return true
	}

	return false
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
