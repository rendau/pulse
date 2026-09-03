package app

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mechta-market/pulse/internal/constant"
)

const mcpInstructions = `pulse — единая точка доступа к инфраструктурному контексту компании: ` +
	`каталог сервисов, их состояние в Kubernetes, метрики, логи и изменения. ` +
	`Все инструменты read-only. Порядок диагностики: resolve_service → get_service_snapshot → при необходимости query_logs / query_metrics / get_service_info; ` +
	`при ошибках источников — ping. Имена сервисов бери только из resolve_service/list_services.`

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
// горизонтально без sticky-сессий.
func MCPHttpServerCreate(port, path, authToken string, server *mcp.Server) *http.Server {
	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, Logger: slog.Default()},
	)

	mux := http.NewServeMux()
	mux.Handle(path, authMiddleware(authToken, handler))

	return &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       time.Minute,
		MaxHeaderBytes:    300 * 1024,
	}
}

// authMiddleware проверяет статический bearer-токен. Пустой токен — проверка отключена
// (локальная разработка); в этом случае в лог пишется предупреждение при старте.
func authMiddleware(token string, next http.Handler) http.Handler {
	if token == "" {
		slog.Warn("MCP_AUTH_TOKEN is empty: mcp endpoint is unauthenticated")
		return next
	}

	expected := []byte(token)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), expected) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="mcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
