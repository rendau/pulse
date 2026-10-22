package app

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/samber/lo"

	"github.com/rendau/pulse/internal/config"
	"github.com/rendau/pulse/internal/constant"
	domainOAuthServiceP "github.com/rendau/pulse/internal/domain/oauth/service"
	"github.com/rendau/pulse/internal/errs"
	usecaseOAuthP "github.com/rendau/pulse/internal/usecase/oauth"
	"github.com/rendau/pulse/internal/util/clientip"
)

const mcpInstructions = `pulse — единая точка доступа к инфраструктурному контексту компании: ` +
	`каталог сервисов, их состояние в Kubernetes, метрики, логи и изменения. ` +
	`Все инструменты read-only. Порядок диагностики: get_cluster_health, если лежит многое; иначе resolve_service → get_service_snapshot → затем get_timeline («что изменилось»), при необходимости query_logs / query_metrics / get_changes / get_service_info; ` +
	`при ошибках источников — ping. Имя, названное человеком, переводи в имя каталога через resolve_service ` +
	`(list_services — если нужен перечень); имена из ответов инструментов (поле service, список services) ` +
	`уже точные — используй как есть, без перепроверки. resolve_service ничего не нашёл — попробуй перевод на английский ` +
	`и синонимы («платежи» → payment, acquiring), затем list_services; не угадывай молча — назови кандидатов и спроси. ` +
	`Ошибки в логах без названного сервиса — get_cluster_health (log_errors), не переспрашивай сервис. ` +
	`Номер заказа, id клиента и другой идентификатор — query_logs без service с pattern: найдёт сервисы и строки ` +
	`(сам ищет назад по дням; названный день или время — в end). Найденные строки — уже все следы по всем сервисам: ` +
	`не запрашивай их повторно по каждому сервису, разве что truncated. ` +
	`Телефон в pattern query_logs — с «+» (+77011234567): найдётся в любом написании. ` +
	`Смысл статусов и «застрял ли объект» — domain в get_service_info (stuck_after); чей номер — id_matches в query_logs. ` +
	`«Сколько объектов сейчас, есть ли застрявшие» — сначала self_reported.entities в get_service_snapshot: сервис сам ` +
	`считает по статусам и порогам domain (stuck; oldest_s — время в статусе, не с создания); ручки — за подробностями. ` +
	`error_rate — сбои сервиса; rejected_rate — отказы по делу (не найдено, неверный запрос или код): не сбой, резкий ` +
	`рост — повод посмотреть, кто шлёт такие запросы. Разбивка по методам и кодам — query_metrics promql с sum by (method, code). ` +
	`Предметные вопросы (где заказ, что в очереди) — diagnostic_endpoints из get_service_info и call_service_endpoint, ` +
	`если сервис их объявил; self_reported в снапшоте — что сервис сообщает о своих зависимостях сам. ` +
	`Причина — у соседа (сервис сообщает о сбое зависимости, ошибки в логах указывают на неё или вопрос — про связь с ней) — ` +
	`загляни к соседу сам, а не останавливайся на «зависимость сбоит» и не советуй человеку его проверить. Имя соседа в каталоге — ` +
	`только service у зависимости (self_reported снапшота, dependencies карточки) или сервис из get_dependencies; target — адрес, ` +
	`а не имя: нет service — соседа в каталоге нет, не угадывай. У соседа — get_service_snapshot, а нужны подробности ` +
	`(какие объекты, базы, запросы сбоят) — ручка из его diagnostic_endpoints. Один шаг по графу, не больше двух соседей; ` +
	`в ответе раздели, что видит сам сервис и что — сосед.`

// MCPServerCreate собирает MCP-сервер и регистрирует инструменты.
func MCPServerCreate(register func(server *mcp.Server)) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: constant.ServiceName, Version: constant.Version},
		&mcp.ServerOptions{Instructions: mcpInstructions, Logger: slog.Default()},
	)

	register(server)

	return server
}

// mcpOAuthSecretMinLen — ключ подписи client_id: короче — подбираем.
const mcpOAuthSecretMinLen = 32

// mcpOAuthConfigCheck — OAuth MCP-порта настроен целиком.
func mcpOAuthConfigCheck() error {
	conf := config.Conf
	if conf.MCPPublicUrl == "" {
		return errors.New("MCP_OAUTH_ENABLED requires MCP_PUBLIC_URL")
	}
	parsed, err := url.Parse(conf.MCPPublicUrl)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return fmt.Errorf("MCP_PUBLIC_URL %q must be an absolute http(s) URL", conf.MCPPublicUrl)
	}
	if len(conf.MCPOAuthSecret) < mcpOAuthSecretMinLen {
		return fmt.Errorf("MCP_OAUTH_ENABLED requires MCP_OAUTH_SECRET of at least %d characters", mcpOAuthSecretMinLen)
	}
	if !lo.ContainsBy(conf.MCPExternalTokens, func(t string) bool { return strings.TrimSpace(t) != "" }) {
		return errors.New("MCP_OAUTH_ENABLED requires MCP_EXTERNAL_TOKENS: a connection is approved with one of them")
	}
	return nil
}

// mcpOAuthI — OAuth на MCP-порту (handler/oauth); nil — OAuth выключен.
type mcpOAuthI interface {
	Register(mux *http.ServeMux)
	ResourceMetadataURL() string
	VerifyAccessToken(ctx context.Context, token string) (time.Time, error)
}

// MCPHttpServerCreate строит HTTP-сервер MCP-порта: streamable-транспорт на любом пути, кроме
// адресов OAuth (с OAuth), — порт отдельный, свой путь MCP не нужен. Режим stateless: нет сессий, каждый запрос самодостаточен — сервис можно
// масштабировать горизонтально без sticky-сессий. internalToken — внутреннего клиента (агент
// pulse: ему — ответы ручек для человека), externalTokens — внешних; пустые игнорируются.
func MCPHttpServerCreate(port, internalToken string, externalTokens []string, oauth mcpOAuthI, server *mcp.Server) *http.Server {
	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, Logger: slog.Default()},
	)

	mux := http.NewServeMux()
	mux.Handle("/", authMiddleware(internalToken, externalTokens, oauth, handler))
	if oauth != nil {
		oauth.Register(mux)
	}

	return &http.Server{
		Addr:              ":" + port,
		Handler:           withAccessLog(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       time.Minute,
		MaxHeaderBytes:    300 * 1024,
	}
}

// authMiddleware проверяет bearer-токен: подходит любой из статических токенов или, с OAuth,
// access-токен OAuth (pso_). Внутренний токен получает право constant.ScopeHuman (инструменты
// видят его в req.Extra.TokenInfo); клиенты OAuth — внешние, этого права у них нет. Нет ни
// одного непустого токена и OAuth выключен — проверка отключена (локальная разработка), ручки
// для человека недоступны; в этом случае в лог пишется предупреждение при старте. С OAuth 401
// несёт WWW-Authenticate с адресом метаданных защищаемого ресурса — с него клиент начинает вход.
func authMiddleware(internalToken string, externalTokens []string, oauth mcpOAuthI, next http.Handler) http.Handler {
	nonEmpty := func(t string, _ int) ([]byte, bool) {
		t = strings.TrimSpace(t)
		return []byte(t), t != ""
	}
	internal := lo.FilterMap([]string{internalToken}, nonEmpty)
	expected := append(slices.Clone(internal), lo.FilterMap(externalTokens, nonEmpty)...)
	if len(expected) == 0 && oauth == nil {
		slog.Warn("MCP_AUTH_TOKEN and MCP_EXTERNAL_TOKENS are empty: mcp endpoint is unauthenticated")
		return next
	}

	verify := func(ctx context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
		if oauth != nil && strings.HasPrefix(token, domainOAuthServiceP.AccessTokenPrefix) {
			expiresAt, err := oauth.VerifyAccessToken(ctx, token)
			if err != nil {
				if errors.Is(err, errs.NotAuthorized) {
					slog.Warn("mcp: credential rejected", "credential", credentialOAuth, "client_ip", clientip.FromRequest(r))
					return nil, auth.ErrInvalidToken
				}
				slog.Error("mcp: oauth token check failed", "error", err)
				return nil, fmt.Errorf("oauth.VerifyAccessToken: %w", err)
			}
			return &auth.TokenInfo{Scopes: []string{usecaseOAuthP.Scope}, Expiration: expiresAt}, nil
		}

		got := []byte(token)
		if !tokenMatch(got, expected) {
			slog.Warn("mcp: credential rejected", "credential", credentialStatic, "client_ip", clientip.FromRequest(r))
			return nil, auth.ErrInvalidToken
		}
		info := &auth.TokenInfo{Scopes: []string{usecaseOAuthP.Scope}}
		if tokenMatch(got, internal) {
			info.Scopes = append(info.Scopes, constant.ScopeHuman)
		}
		return info, nil
	}

	opts := &auth.RequireBearerTokenOptions{AllowMissingExpiration: true}
	if oauth != nil {
		opts.ResourceMetadataURL = oauth.ResourceMetadataURL()
		// у любого принятого токена есть scope mcp: требование только попадает в WWW-Authenticate
		opts.Scopes = []string{usecaseOAuthP.Scope}
	}
	return auth.RequireBearerToken(verify, opts)(next)
}

// tokenMatch сравнивает со всеми токенами за постоянное время — без раннего выхода,
// чтобы время ответа не выдавало, какой из токенов совпал.
func tokenMatch(got []byte, expected [][]byte) bool {
	match := 0
	for _, e := range expected {
		match |= subtle.ConstantTimeCompare(got, e)
	}
	return match == 1
}
