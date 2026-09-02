package app

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/mechta-market/pulse/internal/infra/metrics"
)

var (
	metricRequestCounter   *prometheus.CounterVec
	metricResponseDuration *prometheus.HistogramVec
)

func init() {
	metricRequestCounter = metrics.Factory.NewCounterVec(prometheus.CounterOpts{
		Name: "request_total",
	}, []string{
		"protocol",
		"method",
		"status",
	})

	metricResponseDuration = metrics.Factory.NewHistogramVec(prometheus.HistogramOpts{
		Name: "response_duration_seconds",
		Buckets: []float64{
			0.005,
			0.02,
			0.1,
			0.5,
			2,
		},
	}, []string{
		"protocol",
		"method",
	})
}
