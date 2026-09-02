package app

import (
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/mechta-market/pulse/internal/infra/metrics"
)

const systemHttpPort = 3003

// SystemHttpServerCreate builds the system HTTP server that exposes
// service endpoints: /healthcheck, /docs/*, /metrics.
func SystemHttpServerCreate() *http.Server {
	mux := http.NewServeMux()

	// healthcheck
	mux.HandleFunc("/healthcheck", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// docs
	docFS := http.StripPrefix("/docs/", http.FileServer(http.Dir("./docs")))
	mux.Handle("/docs/", docFS)

	// metrics (uses metrics.Registry instead of the default promhttp registry)
	mux.Handle("/metrics", promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{}))

	return &http.Server{
		Addr:              fmt.Sprintf(":%d", systemHttpPort),
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       time.Minute,
		MaxHeaderBytes:    300 * 1024,
	}
}
