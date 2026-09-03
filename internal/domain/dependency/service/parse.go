package service

import (
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/mechta-market/pulse/internal/domain/dependency/model"
)

var (
	hostPortRe = regexp.MustCompile(`^([a-zA-Z0-9][a-zA-Z0-9.-]*[a-zA-Z0-9]|[a-zA-Z0-9]):(\d{1,5})$`)
	hostOnlyRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*(\.[a-zA-Z0-9][a-zA-Z0-9-]*)+$`)
)

// ParseEndpoints извлекает адреса из значения конфигурации: URL (http://host:8080/path,
// postgres://user:pass@host:5432/db — учётные данные отбрасываются), host:port, списки через
// запятую (kafka-0:9092,kafka-1:9092). Значения без адреса дают пустой результат.
func (s *Service) ParseEndpoints(value string) []model.Endpoint {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 2048 {
		return nil
	}

	result := make([]model.Endpoint, 0, 2)
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if ep, ok := parseOne(part); ok {
			result = append(result, ep)
		}
	}
	return result
}

func parseOne(value string) (model.Endpoint, bool) {
	// URL со схемой
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil || u.Hostname() == "" {
			return model.Endpoint{}, false
		}
		ep := model.Endpoint{Scheme: strings.ToLower(u.Scheme), Host: strings.ToLower(u.Hostname())}
		if p, err := strconv.Atoi(u.Port()); err == nil {
			ep.Port = int32(p)
		}
		if ep.Port == 0 {
			ep.Port = defaultPort(ep.Scheme)
		}
		return ep, isHost(ep.Host)
	}

	// host:port
	if m := hostPortRe.FindStringSubmatch(value); m != nil {
		port, _ := strconv.Atoi(m[2])
		host := strings.ToLower(m[1])
		if port > 0 && port <= 65535 && isHost(host) {
			return model.Endpoint{Host: host, Port: int32(port)}, true
		}
		return model.Endpoint{}, false
	}

	// голый FQDN без порта — адрес, только если это явно хост: три и более сегментов
	// (name.ns.svc, grpc.api.example.com) либо известный TLD; «orders.created» — топик, не хост
	if host := strings.ToLower(value); hostOnlyRe.MatchString(host) && isHost(host) && looksLikeFQDN(host) {
		return model.Endpoint{Host: host}, true
	}

	return model.Endpoint{}, false
}

var knownTLDs = map[string]struct{}{
	"kz": {}, "com": {}, "ru": {}, "io": {}, "net": {}, "org": {}, "dev": {}, "cloud": {}, "local": {}, "svc": {}, "internal": {},
}

func looksLikeFQDN(host string) bool {
	labels := strings.Split(host, ".")
	if len(labels) >= 3 {
		return true
	}
	_, ok := knownTLDs[labels[len(labels)-1]]
	return ok
}

// isHost отсекает то, что похоже на хост, но им не является: версии (1.2.3), localhost,
// одиночные слова без точки уже не проходят регулярки.
func isHost(host string) bool {
	if host == "" || host == "localhost" || strings.HasPrefix(host, "127.") || host == "0.0.0.0" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return true
	}
	// «1.2.3» — версия, не хост: все сегменты числовые, но не IPv4
	allDigits := true
	for _, seg := range strings.Split(host, ".") {
		if seg == "" {
			return false
		}
		if _, err := strconv.Atoi(seg); err != nil {
			allDigits = false
		}
	}
	return !allDigits
}

func defaultPort(scheme string) int32 {
	switch scheme {
	case "http", "ws":
		return 80
	case "https", "wss", "grpcs":
		return 443
	case "postgres", "postgresql":
		return 5432
	case "redis", "rediss":
		return 6379
	case "mysql":
		return 3306
	case "amqp":
		return 5672
	case "mongodb":
		return 27017
	case "kafka":
		return 9092
	case "nats":
		return 4222
	case "clickhouse":
		return 9000
	default:
		return 0
	}
}

// ClusterHost разбирает внутрикластерное имя: name, name.namespace, name.namespace.svc,
// name.namespace.svc.cluster.local → (name, namespace, ok). Внешние хосты — ok=false.
func (s *Service) ClusterHost(host string) (string, string, bool) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if net.ParseIP(host) != nil {
		return "", "", false
	}
	host = strings.TrimSuffix(host, ".cluster.local")
	parts := strings.Split(host, ".")
	switch {
	case len(parts) == 1:
		return parts[0], "", true
	case len(parts) == 2:
		return parts[0], parts[1], true
	case len(parts) == 3 && parts[2] == "svc":
		return parts[0], parts[1], true
	default:
		return "", "", false
	}
}
