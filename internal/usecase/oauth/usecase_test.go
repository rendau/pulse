package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/pulse/internal/domain/oauth/model"
	oauthRepoMem "github.com/rendau/pulse/internal/domain/oauth/repo/mem"
	oauthService "github.com/rendau/pulse/internal/domain/oauth/service"
	"github.com/rendau/pulse/internal/errs"
)

const (
	testIssuer   = "https://pulse-mcp.example.com/mcp"
	testRedirect = "https://claude.ai/api/mcp/auth_callback"
	testVerifier = "verifier-verifier-verifier-verifier-verifier"
	testKey      = "external-key"
)

type testTxm struct{}

func (t *testTxm) TxFn(ctx context.Context, f func(context.Context) error) error {
	return f(ctx)
}

func newTestUsecase(keys ...string) *Usecase {
	return New(
		Config{
			Enabled:      true,
			Issuer:       testIssuer,
			Keys:         keys,
			RedirectUris: []string{testRedirect, "http://localhost"},
			AccessTTL:    time.Hour,
			RefreshTTL:   24 * time.Hour,
		},
		oauthService.New(oauthRepoMem.New()),
		oauthService.NewClientCodec("secret"),
		&testTxm{},
	)
}

func assertErrCode(t *testing.T, err error, code errs.Err) {
	t.Helper()
	full, ok := errors.AsType[errs.ErrFull](err)
	require.True(t, ok, "%v", err)
	assert.Equal(t, code, full.Err)
}

func assertOAuthErr(t *testing.T, err error, code string) {
	t.Helper()
	oauthErr, ok := errors.AsType[*Error](err)
	require.True(t, ok, "%v", err)
	assert.Equal(t, code, oauthErr.Code)
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func register(t *testing.T, uc *Usecase, authMethod string) *RegisterRep {
	t.Helper()
	rep, err := uc.Register(context.Background(), &RegisterReq{
		RedirectUris:            []string{testRedirect},
		ClientName:              "Claude",
		TokenEndpointAuthMethod: authMethod,
	})
	require.NoError(t, err)
	return rep
}

func authorizeReq(clientId string) *AuthorizeReq {
	return &AuthorizeReq{
		ResponseType:        "code",
		ClientId:            clientId,
		RedirectUri:         testRedirect,
		State:               "st",
		CodeChallenge:       pkceChallenge(testVerifier),
		CodeChallengeMethod: "S256",
		Resource:            testIssuer + "/",
	}
}

// approve — согласие с ключом доступа; возвращает код из адреса возврата.
func approve(t *testing.T, uc *Usecase, clientId string) string {
	t.Helper()
	redirect, err := uc.Authorize(context.Background(), authorizeReq(clientId), testKey, true)
	require.NoError(t, err)
	parsed, err := url.Parse(redirect)
	require.NoError(t, err)
	assert.Equal(t, "st", parsed.Query().Get("state"))
	code := parsed.Query().Get("code")
	require.NotEmpty(t, code)
	return code
}

func exchange(uc *Usecase, clientId, code, verifier string) (*TokenRep, error) {
	return uc.Token(context.Background(), &TokenReq{
		GrantType:    "authorization_code",
		Code:         code,
		RedirectUri:  testRedirect,
		CodeVerifier: verifier,
		ClientId:     clientId,
		Resource:     testIssuer,
	})
}

func TestRegister(t *testing.T) {
	uc := newTestUsecase(testKey)

	t.Run("public client", func(t *testing.T) {
		rep := register(t, uc, "none")
		assert.Empty(t, rep.ClientSecret)
		assert.Equal(t, "Claude", rep.Client.Name)
		assert.True(t, strings.HasPrefix(rep.ClientId, "pscl_"))
	})

	t.Run("confidential by default", func(t *testing.T) {
		rep := register(t, uc, "")
		assert.Equal(t, model.ClientAuthSecretBasic, rep.Client.AuthMethod)
		assert.NotEmpty(t, rep.ClientSecret)
	})

	t.Run("foreign redirect", func(t *testing.T) {
		_, err := uc.Register(context.Background(), &RegisterReq{RedirectUris: []string{"https://evil.example/cb"}})
		assertOAuthErr(t, err, ErrInvalidRedirectUri)
	})

	t.Run("loopback any port", func(t *testing.T) {
		_, err := uc.Register(context.Background(), &RegisterReq{
			RedirectUris:            []string{"http://localhost:53682/callback"},
			TokenEndpointAuthMethod: "none",
		})
		require.NoError(t, err)
	})
}

func TestAuthorize(t *testing.T) {
	uc := newTestUsecase(testKey, " ", "")
	client := register(t, uc, "none")

	t.Run("describe", func(t *testing.T) {
		info, err := uc.DescribeAuthorization(context.Background(), authorizeReq(client.ClientId))
		require.NoError(t, err)
		assert.Equal(t, "Claude", info.ClientName)
		assert.Equal(t, "claude.ai", info.RedirectHost)
	})

	for name, key := range map[string]string{"wrong key": "nope", "empty key": "", "blank key": " "} {
		t.Run(name, func(t *testing.T) {
			_, err := uc.Authorize(context.Background(), authorizeReq(client.ClientId), key, true)
			assert.ErrorIs(t, err, errs.OAuthInvalidKey)
		})
	}

	t.Run("key with spaces around", func(t *testing.T) {
		_, err := uc.Authorize(context.Background(), authorizeReq(client.ClientId), " "+testKey+"\n", true)
		require.NoError(t, err)
	})

	t.Run("redirect not registered", func(t *testing.T) {
		req := authorizeReq(client.ClientId)
		req.RedirectUri = "http://localhost:1234/cb"
		_, err := uc.DescribeAuthorization(context.Background(), req)
		assertErrCode(t, err, errs.OAuthInvalidRequest)
	})

	t.Run("forged client", func(t *testing.T) {
		_, err := uc.DescribeAuthorization(context.Background(), authorizeReq("pscl_forged.sig"))
		assertErrCode(t, err, errs.OAuthInvalidRequest)
	})

	t.Run("pkce required", func(t *testing.T) {
		req := authorizeReq(client.ClientId)
		req.CodeChallengeMethod = "plain"
		_, err := uc.DescribeAuthorization(context.Background(), req)
		assertErrCode(t, err, errs.OAuthInvalidRequest)
	})

	t.Run("other resource", func(t *testing.T) {
		req := authorizeReq(client.ClientId)
		req.Resource = "https://other.example.com"
		_, err := uc.DescribeAuthorization(context.Background(), req)
		assertErrCode(t, err, errs.OAuthInvalidRequest)
	})

	t.Run("deny needs no key", func(t *testing.T) {
		redirect, err := uc.Authorize(context.Background(), authorizeReq(client.ClientId), "", false)
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(redirect, testRedirect+"?"))
		assert.Contains(t, redirect, "error=access_denied")
		assert.Contains(t, redirect, "state=st")
	})
}

func TestTokenFlow(t *testing.T) {
	uc := newTestUsecase(testKey, "other-key")
	client := register(t, uc, "none")
	code := approve(t, uc, client.ClientId)

	tokens, err := exchange(uc, client.ClientId, code, testVerifier)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(tokens.AccessToken, oauthService.AccessTokenPrefix))
	assert.True(t, strings.HasPrefix(tokens.RefreshToken, oauthService.RefreshTokenPrefix))
	assert.Equal(t, int64(3600), tokens.ExpiresIn)
	assert.Equal(t, Scope, tokens.Scope)

	t.Run("code is single-use", func(t *testing.T) {
		_, err := exchange(uc, client.ClientId, code, testVerifier)
		assertOAuthErr(t, err, ErrInvalidGrant)
	})

	t.Run("wrong verifier", func(t *testing.T) {
		_, err := exchange(uc, client.ClientId, approve(t, uc, client.ClientId), strings.Repeat("x", 43))
		assertOAuthErr(t, err, ErrInvalidGrant)
	})

	t.Run("code of another client", func(t *testing.T) {
		other := register(t, uc, "none")
		_, err := exchange(uc, other.ClientId, approve(t, uc, client.ClientId), testVerifier)
		assertOAuthErr(t, err, ErrInvalidGrant)
	})

	access, err := uc.AccessFromToken(context.Background(), tokens.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, "Claude", access.ClientName)
	assert.WithinDuration(t, time.Now().Add(time.Hour), access.ExpiresAt, time.Minute)
	grantId := access.GrantId

	t.Run("refresh token is not an access token", func(t *testing.T) {
		_, err := uc.AccessFromToken(context.Background(), tokens.RefreshToken)
		assert.ErrorIs(t, err, errs.NotAuthorized)
	})

	t.Run("refresh rotates", func(t *testing.T) {
		refreshed, err := uc.Token(context.Background(), &TokenReq{
			GrantType:    "refresh_token",
			RefreshToken: tokens.RefreshToken,
			ClientId:     client.ClientId,
		})
		require.NoError(t, err)

		access, err := uc.AccessFromToken(context.Background(), refreshed.AccessToken)
		require.NoError(t, err)
		assert.Equal(t, grantId, access.GrantId)

		_, err = uc.Token(context.Background(), &TokenReq{
			GrantType:    "refresh_token",
			RefreshToken: tokens.RefreshToken,
			ClientId:     client.ClientId,
		})
		assertOAuthErr(t, err, ErrInvalidGrant)

		tokens = refreshed
	})

	t.Run("refresh by another client", func(t *testing.T) {
		other := register(t, uc, "none")
		_, err := uc.Token(context.Background(), &TokenReq{
			GrantType:    "refresh_token",
			RefreshToken: tokens.RefreshToken,
			ClientId:     other.ClientId,
		})
		assertOAuthErr(t, err, ErrInvalidGrant)
	})

	t.Run("revoke", func(t *testing.T) {
		require.NoError(t, uc.Revoke(context.Background(), "psr_unknown", client.ClientId, ""))
		// refresh-токен уже забран неудачным обменом выше (в тесте нет отката транзакции) —
		// отзываем access-токеном: отзыв любого токена удаляет подключение
		require.NoError(t, uc.Revoke(context.Background(), tokens.AccessToken, client.ClientId, ""))

		_, err := uc.AccessFromToken(context.Background(), tokens.AccessToken)
		assert.ErrorIs(t, err, errs.NotAuthorized)
	})
}

// Ключ, которым разрешено подключение, убран из MCP_EXTERNAL_TOKENS (рестарт с новой
// конфигурацией) — подключение больше не работает, ни access, ни refresh.
func TestKeyRemoved(t *testing.T) {
	uc := newTestUsecase(testKey, "other-key")
	client := register(t, uc, "none")
	tokens, err := exchange(uc, client.ClientId, approve(t, uc, client.ClientId), testVerifier)
	require.NoError(t, err)

	restarted := New(
		Config{Enabled: true, Issuer: testIssuer, Keys: []string{"other-key"}, RedirectUris: uc.conf.RedirectUris,
			AccessTTL: time.Hour, RefreshTTL: time.Hour},
		uc.svc, uc.clientCodec, uc.txm,
	)

	_, err = restarted.AccessFromToken(context.Background(), tokens.AccessToken)
	assert.ErrorIs(t, err, errs.NotAuthorized)

	_, err = restarted.Token(context.Background(), &TokenReq{
		GrantType:    "refresh_token",
		RefreshToken: tokens.RefreshToken,
		ClientId:     client.ClientId,
	})
	assertOAuthErr(t, err, ErrInvalidGrant)
}

func TestConfidentialClient(t *testing.T) {
	uc := newTestUsecase(testKey)
	client := register(t, uc, model.ClientAuthSecretPost)

	req := &TokenReq{
		GrantType:    "authorization_code",
		Code:         approve(t, uc, client.ClientId),
		RedirectUri:  testRedirect,
		CodeVerifier: testVerifier,
		ClientId:     client.ClientId,
	}

	_, err := uc.Token(context.Background(), req)
	assertOAuthErr(t, err, ErrInvalidClient)

	req.Code = approve(t, uc, client.ClientId)
	req.ClientSecret = client.ClientSecret
	_, err = uc.Token(context.Background(), req)
	require.NoError(t, err)
}

func TestDisabled(t *testing.T) {
	uc := newTestUsecase(testKey)
	uc.conf.Enabled = false

	_, err := uc.Register(context.Background(), &RegisterReq{RedirectUris: []string{testRedirect}})
	assert.Equal(t, errNotEnabled, err)

	_, err = uc.AccessFromToken(context.Background(), "pso_x")
	assert.ErrorIs(t, err, errs.NotAuthorized)
}
