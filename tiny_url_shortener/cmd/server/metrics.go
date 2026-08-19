package main

import (
	"log"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"kyoku.dev/system-design-playground/tiny_url_shortener/internal/shortener"
)

func newMetricsHandlers(application http.Handler, visits *shortener.VisitBuffer) (http.Handler, http.Handler) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "tiny_url_shortener",
		Name:      "http_requests_total",
		Help:      "Total application HTTP requests.",
	}, []string{"code", "method"})
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "tiny_url_shortener",
		Name:      "http_request_duration_seconds",
		Help:      "Application HTTP request duration in seconds.",
		Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 1},
	}, []string{"code", "method"})
	registry.MustRegister(requests, duration)

	if visits != nil {
		registry.MustRegister(
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Namespace: "tiny_url_shortener",
				Name:      "visit_buffer_dropped_total",
				Help:      "Total visits dropped from the in-memory buffer.",
			}, func() float64 { return float64(visits.DroppedVisits()) }),
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Namespace: "tiny_url_shortener",
				Name:      "visit_buffer_flush_failures_total",
				Help:      "Total failed visit buffer flush attempts.",
			}, func() float64 { return float64(visits.FlushFailures()) }),
			prometheus.NewGaugeFunc(prometheus.GaugeOpts{
				Namespace: "tiny_url_shortener",
				Name:      "visit_buffer_pending_keys",
				Help:      "Current URL keys waiting in memory, including failed flush batches.",
			}, func() float64 { return float64(visits.PendingKeys()) }),
		)
	}

	instrumented := promhttp.InstrumentHandlerDuration(duration,
		promhttp.InstrumentHandlerCounter(requests, application))
	metrics := http.NewServeMux()
	metrics.Handle("GET /metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{ErrorLog: log.Default()}))
	return instrumented, metrics
}
