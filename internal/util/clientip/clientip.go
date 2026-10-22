// Package clientip — адрес клиента запроса. К MCP-порту клиенты приходят через Cloudflare и
// ingress, поэтому адрес берётся из их заголовков.
package clientip

import (
	"net"
	"net/http"
	"strings"
)

// FromRequest: Cf-Connecting-Ip → первый из X-Forwarded-For → X-Real-Ip → адрес соединения.
func FromRequest(r *http.Request) string {
	if v := r.Header.Get("Cf-Connecting-Ip"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		first, _, _ := strings.Cut(v, ",")
		return strings.TrimSpace(first)
	}
	if v := r.Header.Get("X-Real-Ip"); v != "" {
		return v
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
