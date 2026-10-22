package app

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	domainOAuthServiceP "github.com/rendau/pulse/internal/domain/oauth/service"
	"github.com/rendau/pulse/internal/util/clientip"
)

// виды учётных данных в журнале MCP-порта (сами значения не пишутся никогда)
const (
	credentialNone   = "none"
	credentialStatic = "static"
	credentialOAuth  = "oauth"
)

// rpcPeekMaxBytes ограничивает, сколько тела JSON-RPC читается заранее ради имени метода; тело
// больше уходит дальше как есть и пишется без метода.
const rpcPeekMaxBytes = 64 * 1024

// statusRecorder запоминает статус ответа для журнала. Unwrap даёт http.ResponseController
// добраться до Flush потоковых (SSE) ответов.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

// withAccessLog пишет каждый запрос MCP-порта. Снаружи клиенты приходят через Cloudflare и
// ingress — адрес берётся из их заголовков; cf_ray находит запрос в событиях безопасности
// Cloudflare.
func withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rpcMethod := peekRPCMethod(r)
		rec := &statusRecorder{ResponseWriter: w}

		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		slog.Info("mcp: request",
			"method", r.Method,
			"path", r.URL.Path,
			"rpc_method", rpcMethod,
			"status", status,
			"duration_ms", time.Since(started).Milliseconds(),
			"credential", credentialKind(r),
			"client_ip", clientip.FromRequest(r),
			"cf_ray", r.Header.Get("Cf-Ray"),
			"user_agent", r.UserAgent(),
		)
	})
}

// peekRPCMethod читает метод JSON-RPC (или методы пакета) у POST и возвращает прочитанное
// в начало тела.
func peekRPCMethod(r *http.Request) string {
	if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return ""
	}

	head, err := io.ReadAll(io.LimitReader(r.Body, rpcPeekMaxBytes+1))
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head), r.Body), r.Body}
	if err != nil || len(head) > rpcPeekMaxBytes {
		return ""
	}

	type rpcMessage struct {
		Method string `json:"method"`
	}

	head = bytes.TrimSpace(head)
	if bytes.HasPrefix(head, []byte("[")) {
		var batch []rpcMessage
		if json.Unmarshal(head, &batch) != nil {
			return ""
		}
		methods := make([]string, 0, len(batch))
		for _, m := range batch {
			methods = append(methods, m.Method)
		}
		return strings.Join(methods, ",")
	}

	var msg rpcMessage
	if json.Unmarshal(head, &msg) != nil {
		return ""
	}
	return msg.Method
}

func credentialKind(r *http.Request) string {
	fields := strings.Fields(r.Header.Get("Authorization"))
	switch {
	case len(fields) != 2 || !strings.EqualFold(fields[0], "bearer"):
		return credentialNone
	case strings.HasPrefix(fields[1], domainOAuthServiceP.AccessTokenPrefix):
		return credentialOAuth
	}
	return credentialStatic
}
