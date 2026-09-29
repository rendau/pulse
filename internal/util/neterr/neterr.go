// Package neterr — вид сетевой ошибки словами: почему сервис не ответил. По нему видно, где
// искать причину: таймаут — сеть или сервис висит, отклонено — порт не слушается, DNS — нет
// такого имени, нет готовых подов — за Service некому ответить.
package neterr

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
)

const (
	Timeout     = "таймаут"
	Refused     = "соединение отклонено"
	Reset       = "соединение сброшено"
	Unreachable = "хост недоступен"
	DnsNotFound = "DNS: имя не найдено"
	Dns         = "ошибка DNS"
	NoEndpoints = "нет готовых подов за Service"
)

// proxyReasons — ошибки прокси API-сервера (локальный режим services/proxy): приходят только
// текстом, в котором API-сервер пересказывает сетевую ошибку.
var proxyReasons = []struct{ text, reason string }{
	{"no endpoints available", NoEndpoints},
	{"connection refused", Refused},
	{"connection reset", Reset},
	{"no such host", DnsNotFound},
	{"no route to host", Unreachable},
	{"network is unreachable", Unreachable},
	{"timeout", Timeout},
	{"deadline exceeded", Timeout},
}

// Reason — вид сетевой ошибки; не распознан — пусто.
func Reason(err error) string {
	if err == nil {
		return ""
	}
	// DNS — первым: у него бывает и свой таймаут
	if dnsErr, ok := errors.AsType[*net.DNSError](err); ok {
		if dnsErr.IsNotFound {
			return DnsNotFound
		}
		return Dns
	}
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return Refused
	case errors.Is(err, syscall.ECONNRESET):
		return Reset
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return Unreachable
	case errors.Is(err, context.DeadlineExceeded):
		return Timeout
	}
	if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
		return Timeout
	}

	msg := strings.ToLower(err.Error())
	for _, r := range proxyReasons {
		if strings.Contains(msg, r.text) {
			return r.reason
		}
	}
	return ""
}
