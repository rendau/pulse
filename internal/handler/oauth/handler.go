// Package oauth — HTTP-транспорт сервера авторизации OAuth 2.1 на MCP-порту: метаданные
// (RFC 9728, RFC 8414), регистрация клиента (RFC 7591), выдача и отзыв токенов, страница
// согласия с ключом доступа.
package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rendau/pulse/internal/constant"
	usecaseOAuth "github.com/rendau/pulse/internal/usecase/oauth"
	"github.com/rendau/pulse/internal/util/clientip"
)

const (
	wellKnownResource   = "/.well-known/oauth-protected-resource"
	wellKnownAuthServer = "/.well-known/oauth-authorization-server"
	wellKnownOpenId     = "/.well-known/openid-configuration"

	pathAuthorize = "/authorize"
	pathRegister  = "/register"
	pathToken     = "/token"
	pathRevoke    = "/revoke"

	formMaxBytes = 64 * 1024
)

type resourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	ScopesSupported        []string `json:"scopes_supported"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
	ResourceName           string   `json:"resource_name"`
}

type authServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint"`
	RevocationEndpoint                string   `json:"revocation_endpoint"`
	ScopesSupported                   []string `json:"scopes_supported"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
}

type registerIn struct {
	RedirectUris            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

type registerOut struct {
	ClientId                string   `json:"client_id"`
	ClientSecret            string   `json:"client_secret,omitempty"`
	ClientIdIssuedAt        int64    `json:"client_id_issued_at"`
	ClientSecretExpiresAt   *int64   `json:"client_secret_expires_at,omitempty"`
	ClientName              string   `json:"client_name"`
	RedirectUris            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

type tokenOut struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

type errorOut struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// Handler — адреса OAuth на MCP-порту. issuer — публичный адрес MCP-сервера; его путь
// (если порт опубликован под путём, напр. https://host/pulse) префиксует каждый адрес.
type Handler struct {
	uc       *usecaseOAuth.Usecase
	issuer   string
	basePath string
}

func New(uc *usecaseOAuth.Usecase) *Handler {
	issuer := strings.TrimSuffix(uc.Config().Issuer, "/")

	basePath := ""
	if parsed, err := url.Parse(issuer); err == nil {
		basePath = strings.TrimSuffix(parsed.Path, "/")
	}

	return &Handler{uc: uc, issuer: issuer, basePath: basePath}
}

// ResourceMetadataURL — для WWW-Authenticate ответа 401: с него клиент начинает вход. Лежит
// под путём сервера, поэтому доходит до pulse, даже если наружу опубликован только этот путь.
func (h *Handler) ResourceMetadataURL() string {
	return h.issuer + wellKnownResource
}

// VerifyAccessToken — срок действия access-токена OAuth; ошибка — токен не принимается.
func (h *Handler) VerifyAccessToken(ctx context.Context, token string) (time.Time, error) {
	access, err := h.uc.AccessFromToken(ctx, token)
	if err != nil {
		return time.Time{}, err
	}
	return access.ExpiresAt, nil
}

func (h *Handler) Register(mux *http.ServeMux) {
	// путь сервера и в форме RFC 8414/9728 (суффикс после well-known), и перед well-known, и
	// без него: часть клиентов ищет метаданные в корне домена. Пустой basePath — одни и те же пути.
	patterns := map[string]http.HandlerFunc{}
	for _, path := range []string{
		h.basePath + wellKnownResource, wellKnownResource + h.basePath, wellKnownResource,
	} {
		patterns[path] = h.handleResourceMetadata
	}
	for _, wellKnown := range []string{wellKnownAuthServer, wellKnownOpenId} {
		for _, path := range []string{h.basePath + wellKnown, wellKnown + h.basePath, wellKnown} {
			patterns[path] = h.handleAuthServerMetadata
		}
	}
	patterns[h.basePath+pathRegister] = h.handleRegister
	patterns[h.basePath+pathToken] = h.handleToken
	patterns[h.basePath+pathRevoke] = h.handleRevoke

	for path, handler := range patterns {
		mux.Handle(path, withCORS(handler))
	}

	// страница согласия — для браузера человека, без CORS
	mux.HandleFunc(h.basePath+pathAuthorize, h.handleAuthorize)
}

func (h *Handler) handleResourceMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, &resourceMetadata{
		Resource:               h.issuer,
		AuthorizationServers:   []string{h.issuer},
		ScopesSupported:        []string{usecaseOAuth.Scope},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           constant.ServiceName + " — инфраструктурный контекст",
	})
}

func (h *Handler) handleAuthServerMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, &authServerMetadata{
		Issuer:                            h.issuer,
		AuthorizationEndpoint:             h.issuer + pathAuthorize,
		TokenEndpoint:                     h.issuer + pathToken,
		RegistrationEndpoint:              h.issuer + pathRegister,
		RevocationEndpoint:                h.issuer + pathRevoke,
		ScopesSupported:                   []string{usecaseOAuth.Scope},
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code", "refresh_token"},
		TokenEndpointAuthMethodsSupported: []string{"none", "client_secret_post", "client_secret_basic"},
		CodeChallengeMethodsSupported:     []string{"S256"},
	})
}

func (h *Handler) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	in := &registerIn{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, formMaxBytes)).Decode(in); err != nil {
		writeOAuthError(w, r, &usecaseOAuth.Error{Code: usecaseOAuth.ErrInvalidClientMetadata, Description: "body must be a JSON object"})
		return
	}

	rep, err := h.uc.Register(r.Context(), &usecaseOAuth.RegisterReq{
		RedirectUris:            in.RedirectUris,
		ClientName:              in.ClientName,
		TokenEndpointAuthMethod: in.TokenEndpointAuthMethod,
		GrantTypes:              in.GrantTypes,
		ResponseTypes:           in.ResponseTypes,
	})
	if err != nil {
		writeOAuthError(w, r, err)
		return
	}

	out := &registerOut{
		ClientId:                rep.ClientId,
		ClientSecret:            rep.ClientSecret,
		ClientIdIssuedAt:        rep.IssuedAt.Unix(),
		ClientName:              rep.Client.Name,
		RedirectUris:            rep.Client.RedirectUris,
		TokenEndpointAuthMethod: rep.Client.AuthMethod,
		GrantTypes:              rep.GrantTypes,
		ResponseTypes:           rep.ResponseTypes,
	}
	if rep.ClientSecret != "" {
		out.ClientSecretExpiresAt = new(int64(0)) // не истекает
	}

	slog.Info("mcp oauth: client registered",
		"client_name", rep.Client.Name,
		"redirect_uris", rep.Client.RedirectUris,
		"auth_method", rep.Client.AuthMethod,
		"client_ip", clientip.FromRequest(r),
	)

	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, formMaxBytes)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, r, &usecaseOAuth.Error{Code: usecaseOAuth.ErrInvalidRequest, Description: "body must be a form"})
		return
	}

	clientId, clientSecret := clientCredentials(r)

	rep, err := h.uc.Token(r.Context(), &usecaseOAuth.TokenReq{
		GrantType:    r.PostForm.Get("grant_type"),
		Code:         r.PostForm.Get("code"),
		RedirectUri:  r.PostForm.Get("redirect_uri"),
		CodeVerifier: r.PostForm.Get("code_verifier"),
		RefreshToken: r.PostForm.Get("refresh_token"),
		Resource:     r.PostForm.Get("resource"),
		ClientId:     clientId,
		ClientSecret: clientSecret,
	})
	if err != nil {
		writeOAuthError(w, r, err)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, &tokenOut{
		AccessToken:  rep.AccessToken,
		TokenType:    "Bearer",
		ExpiresIn:    rep.ExpiresIn,
		RefreshToken: rep.RefreshToken,
		Scope:        rep.Scope,
	})
}

func (h *Handler) handleRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, formMaxBytes)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, r, &usecaseOAuth.Error{Code: usecaseOAuth.ErrInvalidRequest, Description: "body must be a form"})
		return
	}

	clientId, clientSecret := clientCredentials(r)

	if err := h.uc.Revoke(r.Context(), r.PostForm.Get("token"), clientId, clientSecret); err != nil {
		writeOAuthError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// clientCredentials — клиент из HTTP basic (client_secret_basic) или из формы
// (client_secret_post, публичные клиенты).
func clientCredentials(r *http.Request) (string, string) {
	if id, secret, ok := r.BasicAuth(); ok {
		// RFC 6749, 2.3.1: оба значения перед basic-кодированием в form-urlencoded
		if v, err := url.QueryUnescape(id); err == nil {
			id = v
		}
		if v, err := url.QueryUnescape(secret); err == nil {
			secret = v
		}
		return id, secret
	}
	return r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
}

func writeOAuthError(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "no-store")

	oauthErr, ok := errors.AsType[*usecaseOAuth.Error](err)
	if !ok {
		slog.Error("mcp oauth: request failed", "path", r.URL.Path, "client_ip", clientip.FromRequest(r), "error", err)
		writeJSON(w, http.StatusInternalServerError, &errorOut{Error: "server_error", ErrorDescription: "internal error"})
		return
	}

	slog.Warn("mcp oauth: request rejected",
		"path", r.URL.Path,
		"grant_type", r.PostForm.Get("grant_type"),
		"error", oauthErr.Code,
		"description", oauthErr.Description,
		"client_ip", clientip.FromRequest(r),
	)

	if oauthErr.Code == usecaseOAuth.ErrInvalidClient {
		w.Header().Set("WWW-Authenticate", `Basic realm="pulse"`)
	}
	writeJSON(w, oauthErr.HTTPStatus(), &errorOut{Error: oauthErr.Code, ErrorDescription: oauthErr.Description})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("mcp oauth: write response", "error", err)
	}
}

// withCORS — метаданные и выдачу токенов могут вызывать MCP-клиенты в браузере (MCP Inspector);
// куки они не несут, поэтому подходит любой origin.
func withCORS(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, MCP-Protocol-Version")
		w.Header().Set("Access-Control-Max-Age", "86400")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	})
}
