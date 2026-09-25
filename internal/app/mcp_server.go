package app

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/samber/lo"

	"github.com/mechta-market/pulse/internal/constant"
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
	`(сам ищет назад по дням; названный день или время — в end).`

// MCPServerCreate собирает MCP-сервер и регистрирует инструменты.
func MCPServerCreate(register func(server *mcp.Server)) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: constant.ServiceName, Version: constant.Version},
		&mcp.ServerOptions{Instructions: mcpInstructions, Logger: slog.Default()},
	)

	register(server)

	return server
}

// MCPHttpServerCreate строит HTTP-сервер с MCP streamable-транспортом на path.
// Режим stateless: нет сессий, каждый запрос самодостаточен — сервис можно масштабировать
// горизонтально без sticky-сессий. authTokens — допустимые bearer-токены (пустые
// игнорируются).
func MCPHttpServerCreate(port, path string, authTokens []string, server *mcp.Server) *http.Server {
	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, Logger: slog.Default()},
	)

	mux := http.NewServeMux()
	mux.Handle(path, authMiddleware(authTokens, handler))

	return &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       time.Minute,
		MaxHeaderBytes:    300 * 1024,
	}
}

// authMiddleware проверяет bearer-токен: подходит любой из tokens. Нет ни одного
// непустого — проверка отключена (локальная разработка); в этом случае в лог пишется
// предупреждение при старте.
func authMiddleware(tokens []string, next http.Handler) http.Handler {
	expected := lo.FilterMap(tokens, func(t string, _ int) ([]byte, bool) {
		t = strings.TrimSpace(t)
		return []byte(t), t != ""
	})
	if len(expected) == 0 {
		slog.Warn("MCP_AUTH_TOKEN and MCP_EXTERNAL_TOKENS are empty: mcp endpoint is unauthenticated")
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || !tokenMatch([]byte(got), expected) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="mcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
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
