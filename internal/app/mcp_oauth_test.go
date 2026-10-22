package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/pulse/internal/config"
	"github.com/rendau/pulse/internal/constant"
	domainOAuthRepoMemP "github.com/rendau/pulse/internal/domain/oauth/repo/mem"
	domainOAuthServiceP "github.com/rendau/pulse/internal/domain/oauth/service"
	handlerOAuthP "github.com/rendau/pulse/internal/handler/oauth"
	usecaseOAuthP "github.com/rendau/pulse/internal/usecase/oauth"
)

type noTxm struct{}

func (noTxm) TxFn(ctx context.Context, f func(context.Context) error) error { return f(ctx) }

// Полный вход коннектора: 401 с адресом метаданных → метаданные → регистрация → страница
// согласия с ключом → код → токены → вызов инструмента с access-токеном (без права human) →
// refresh → revoke.
func TestMCPServer_OAuth(t *testing.T) {
	ctx := context.Background()
	const internalToken, externalToken = "internal-token", "external-token"
	const redirectUri = "http://localhost:18099/callback"
	verifier := strings.Repeat("v", 43)

	server := MCPServerCreate(func(server *mcp.Server) {
		mcp.AddTool(server, &mcp.Tool{Name: "whoami"}, func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct {
			Scopes []string `json:"scopes"`
		}, error) {
			var out struct {
				Scopes []string `json:"scopes"`
			}
			if req.Extra != nil && req.Extra.TokenInfo != nil {
				out.Scopes = req.Extra.TokenInfo.Scopes
			}
			return nil, out, nil
		})
	})

	// адрес сервера известен только после старта: обработчик подставляется следом
	var handler http.Handler
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	defer ts.Close()
	issuer := ts.URL

	uc := usecaseOAuthP.New(
		usecaseOAuthP.Config{
			Enabled: true, Issuer: issuer, Keys: []string{externalToken},
			RedirectUris: []string{"http://localhost"}, AccessTTL: time.Hour, RefreshTTL: time.Hour,
		},
		domainOAuthServiceP.New(domainOAuthRepoMemP.New()),
		domainOAuthServiceP.NewClientCodec(strings.Repeat("s", 32)),
		noTxm{},
	)
	handler = MCPHttpServerCreate("0", internalToken, []string{externalToken}, handlerOAuthP.New(uc), server).Handler

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// 401 ведёт к метаданным защищаемого ресурса
	resp, err := http.Post(issuer, "application/json", nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("WWW-Authenticate"), `resource_metadata="`+issuer+`/.well-known/oauth-protected-resource"`)

	for _, path := range []string{"/.well-known/oauth-protected-resource"} {
		var meta struct {
			Resource             string   `json:"resource"`
			AuthorizationServers []string `json:"authorization_servers"`
		}
		getJSON(t, ts.URL+path, &meta)
		assert.Equal(t, issuer, meta.Resource, path)
		assert.Equal(t, []string{issuer}, meta.AuthorizationServers, path)
	}
	var asMeta struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
		RegistrationEndpoint  string `json:"registration_endpoint"`
	}
	getJSON(t, ts.URL+"/.well-known/oauth-authorization-server", &asMeta)
	assert.Equal(t, issuer+"/authorize", asMeta.AuthorizationEndpoint)

	// регистрация
	resp, err = http.Post(asMeta.RegistrationEndpoint, "application/json", strings.NewReader(
		`{"redirect_uris":["`+redirectUri+`"],"client_name":"Claude Code","token_endpoint_auth_method":"none"}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var client struct {
		ClientId string `json:"client_id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&client))
	_ = resp.Body.Close()

	sum := sha256.Sum256([]byte(verifier))
	authQuery := url.Values{
		"response_type": {"code"}, "client_id": {client.ClientId}, "redirect_uri": {redirectUri}, "state": {"st"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
		"resource": {issuer},
	}

	// страница согласия
	resp, err = http.Get(asMeta.AuthorizationEndpoint + "?" + authQuery.Encode())
	require.NoError(t, err)
	page, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(page), "Claude Code")
	assert.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))

	consent := func(key, action string) *http.Response {
		form := url.Values{"key": {key}, "action": {action}}
		for k, v := range authQuery {
			form[k] = v
		}
		resp, err := noRedirect.PostForm(asMeta.AuthorizationEndpoint, form)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp
	}

	// внутренний токен агента подключение не разрешает
	assert.Equal(t, http.StatusForbidden, consent(internalToken, "allow").StatusCode)

	resp = consent(externalToken, "allow")
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	location, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, "st", location.Query().Get("state"))

	tokens := postToken(t, asMeta.TokenEndpoint, url.Values{
		"grant_type": {"authorization_code"}, "code": {location.Query().Get("code")}, "redirect_uri": {redirectUri},
		"code_verifier": {verifier}, "client_id": {client.ClientId}, "resource": {issuer},
	})
	assert.True(t, strings.HasPrefix(tokens.AccessToken, domainOAuthServiceP.AccessTokenPrefix))

	whoami := func(token string) ([]string, error) {
		session, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint:   issuer,
			HTTPClient: &http.Client{Transport: &bearerTransport{token: token}},
		}, nil)
		if err != nil {
			return nil, err
		}
		defer func() { _ = session.Close() }()
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "whoami"})
		if err != nil {
			return nil, err
		}
		var out struct {
			Scopes []string `json:"scopes"`
		}
		raw, _ := json.Marshal(res.StructuredContent)
		require.NoError(t, json.Unmarshal(raw, &out))
		return out.Scopes, nil
	}

	scopes, err := whoami(tokens.AccessToken)
	require.NoError(t, err)
	assert.False(t, slices.Contains(scopes, constant.ScopeHuman), "клиент OAuth — внешний")

	refreshed := postToken(t, asMeta.TokenEndpoint, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {tokens.RefreshToken}, "client_id": {client.ClientId},
	})
	_, err = whoami(refreshed.AccessToken)
	require.NoError(t, err)

	resp, err = http.PostForm(issuer+"/revoke", url.Values{"token": {refreshed.RefreshToken}, "client_id": {client.ClientId}})
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	_, err = whoami(refreshed.AccessToken)
	assert.Error(t, err, "подключение отозвано")
}

type tokenRep struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func postToken(t *testing.T, endpoint string, form url.Values) *tokenRep {
	t.Helper()
	resp, err := http.PostForm(endpoint, form)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	rep := &tokenRep{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(rep))
	return rep
}

func getJSON(t *testing.T, endpoint string, v any) {
	t.Helper()
	resp, err := http.Get(endpoint)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode, endpoint)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(v))
}

func TestMCPOAuthConfigCheck(t *testing.T) {
	saved := config.Conf
	defer func() { config.Conf = saved }()

	valid := func() {
		config.Conf.MCPPublicUrl = "https://pulse-mcp.example.com"
		config.Conf.MCPOAuthSecret = strings.Repeat("s", 32)
		config.Conf.MCPExternalTokens = []string{"key"}
	}

	valid()
	require.NoError(t, mcpOAuthConfigCheck())

	for name, spoil := range map[string]func(){
		"no public url":    func() { config.Conf.MCPPublicUrl = "" },
		"relative url":     func() { config.Conf.MCPPublicUrl = "/mcp" },
		"short secret":     func() { config.Conf.MCPOAuthSecret = "short" },
		"no external keys": func() { config.Conf.MCPExternalTokens = []string{" ", ""} },
	} {
		valid()
		spoil()
		assert.Error(t, mcpOAuthConfigCheck(), name)
	}
}
