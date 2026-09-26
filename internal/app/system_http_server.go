package app

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/rendau/pulse/internal/infra/metrics"
)

// SystemHttpServerCreate builds the system HTTP server that exposes
// service endpoints: /healthcheck, /readiness, /metrics and the pulse manifest
// (/.well-known/pulse, /.well-known/pulse/status, /diag/*).
func SystemHttpServerCreate(port string, ready func(ctx context.Context) error, register func(mux *http.ServeMux)) *http.Server {
	mux := http.NewServeMux()
	register(mux)

	// healthcheck: процесс жив
	mux.HandleFunc("/healthcheck", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// readiness: сервис готов принимать запросы (хранилище индекса доступно)
	mux.HandleFunc("/readiness", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if err := ready(ctx); err != nil {
			http.Error(w, "not ready: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	// metrics (uses metrics.Registry instead of the default promhttp registry)
	mux.Handle("/metrics", promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{}))

	return &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       time.Minute,
		MaxHeaderBytes:    300 * 1024,
	}
}
