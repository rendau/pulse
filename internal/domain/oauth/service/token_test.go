package service

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/pulse/internal/domain/oauth/model"
)

func TestRedirectAllowed(t *testing.T) {
	allowed := []string{
		"https://claude.ai/api/mcp/auth_callback",
		"https://any-path.example.com",
		"http://localhost",
		"http://127.0.0.1",
	}

	cases := []struct {
		uri  string
		want bool
	}{
		{"https://claude.ai/api/mcp/auth_callback", true},
		{"https://claude.ai/api/mcp/auth_callback?x=1", true},
		{"https://claude.ai/other", false},
		{"https://claude.ai:8443/api/mcp/auth_callback", false},
		{"http://claude.ai/api/mcp/auth_callback", false},
		{"https://evil.example/api/mcp/auth_callback", false},
		{"https://claude.ai.evil.example/api/mcp/auth_callback", false},
		{"https://user@claude.ai/api/mcp/auth_callback", false},
		{"https://claude.ai/api/mcp/auth_callback#frag", false},
		{"https://any-path.example.com/cb", true},
		{"http://localhost:51234/callback", true},
		{"http://127.0.0.1:8080/cb", true},
		{"https://localhost:51234/callback", false},
		{"not a url", false},
		{"", false},
	}

	for _, c := range cases {
		assert.Equal(t, c.want, RedirectAllowed(allowed, c.uri), c.uri)
	}
}

func TestVerifyPKCE(t *testing.T) {
	verifier := strings.Repeat("a", 43)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	assert.True(t, VerifyPKCE(challenge, verifier))
	assert.False(t, VerifyPKCE(challenge, strings.Repeat("b", 43)))
	assert.False(t, VerifyPKCE(challenge, "short"))
	assert.False(t, VerifyPKCE("", verifier))
}

func TestGenerateToken(t *testing.T) {
	token, hash, err := GenerateToken(model.TokenKindAccess)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(token, AccessTokenPrefix))
	assert.Equal(t, HashToken(token), hash)

	other, _, err := GenerateToken(model.TokenKindAccess)
	require.NoError(t, err)
	assert.NotEqual(t, token, other)
}

func TestClientCodec(t *testing.T) {
	codec := NewClientCodec("secret")
	client := &model.Client{
		Name:         "Claude",
		RedirectUris: []string{"https://claude.ai/api/mcp/auth_callback"},
		AuthMethod:   model.ClientAuthSecretPost,
	}

	id, err := codec.Encode(client)
	require.NoError(t, err)

	decoded, ok := codec.Decode(id)
	require.True(t, ok)
	assert.Equal(t, client, decoded)

	t.Run("tampered payload", func(t *testing.T) {
		payload, sig, _ := strings.Cut(strings.TrimPrefix(id, clientIdPrefix), ".")
		raw, _ := base64.RawURLEncoding.DecodeString(payload)
		forged := strings.Replace(string(raw), "claude.ai", "evil.example", 1)
		forgedId := clientIdPrefix + base64.RawURLEncoding.EncodeToString([]byte(forged)) + "." + sig

		_, ok := codec.Decode(forgedId)
		assert.False(t, ok)
	})

	t.Run("other installation", func(t *testing.T) {
		_, ok := NewClientCodec("another secret").Decode(id)
		assert.False(t, ok)
	})

	t.Run("secret", func(t *testing.T) {
		assert.True(t, codec.VerifySecret(id, codec.Secret(id)))
		assert.False(t, codec.VerifySecret(id, ""))
		assert.False(t, codec.VerifySecret(id, "wrong"))
		assert.NotEqual(t, codec.Secret(id), id)
	})
}
