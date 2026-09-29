package neterr

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Настоящие сетевые ошибки — обёрнутые, как их отдаёт вызов ручки сервиса.
func TestReason_Real(t *testing.T) {
	wrap := func(err error) error {
		return fmt.Errorf("service_not_available: 127.0.0.1/.well-known/pulse: %w", err)
	}

	// порт не слушается
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closed := l.Addr().String()
	require.NoError(t, l.Close())
	_, err = (&net.Dialer{Timeout: time.Second}).DialContext(context.Background(), "tcp", closed)
	require.Error(t, err)
	assert.Equal(t, Refused, Reason(wrap(err)))

	// соединение есть, ответа нет
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/.well-known/pulse", nil)
	require.NoError(t, err)
	resp, err := srv.Client().Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	assert.Equal(t, Timeout, Reason(wrap(err)))
}

func TestReason(t *testing.T) {
	wrap := func(err error) error {
		return fmt.Errorf("service_not_available: pulse.default.svc:3003/.well-known/pulse: %w", err)
	}
	dial := func(err error) error { return wrap(&net.OpError{Op: "dial", Net: "tcp", Err: err}) }

	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "нет ошибки"},
		{name: "таймаут соединения (hairpin)", err: dial(os.ErrDeadlineExceeded), want: Timeout},
		{name: "дедлайн контекста", err: wrap(context.DeadlineExceeded), want: Timeout},
		{name: "порт не слушается", err: dial(os.NewSyscallError("connect", syscall.ECONNREFUSED)), want: Refused},
		{name: "сброс", err: wrap(&net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ECONNRESET)}), want: Reset},
		{name: "нет маршрута", err: dial(os.NewSyscallError("connect", syscall.EHOSTUNREACH)), want: Unreachable},
		{name: "нет такого Service", err: dial(&net.DNSError{Err: "no such host", Name: "ocenter.prod.svc", IsNotFound: true}), want: DnsNotFound},
		{name: "DNS не ответил", err: dial(&net.DNSError{Err: "i/o timeout", Name: "ocenter.prod.svc", IsTimeout: true}), want: Dns},
		{name: "прокси: нет готовых подов", err: fmt.Errorf("Services.ProxyGet(prod/ocenter:3003/.well-known/pulse): %w",
			errors.New(`no endpoints available for service "ocenter:3003"`)), want: NoEndpoints},
		{name: "прокси: порт не слушается", err: errors.New("error trying to reach service: dial tcp 10.1.3.156:3003: connect: connection refused"), want: Refused},
		{name: "прокси: таймаут", err: errors.New("error trying to reach service: dial tcp 10.1.3.156:3003: i/o timeout"), want: Timeout},
		{name: "не сетевая", err: wrap(errors.New("tls: handshake failure"))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, Reason(c.err))
		})
	}
}
