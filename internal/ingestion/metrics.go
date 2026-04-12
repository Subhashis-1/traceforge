// Package ingestion provides ingestion pipeline and metrics for Traceforge.
package ingestion

import (
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	metricsOnce sync.Once
	metricsSet  *metrics
)

type metrics struct {
	ingestRequestsTotal          prometheus.Counter
	ingestFailedTotal            prometheus.Counter
	ingestBatchDurationSeconds   prometheus.Histogram
	cassandraWriteLatencySeconds prometheus.Histogram
	ingestQueueOverflowTotal     prometheus.Counter
}

func getMetrics() *metrics {
	metricsOnce.Do(func() {
		metricsSet = &metrics{
			ingestRequestsTotal: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "ingest_requests_total",
				Help: "Total number of ingestion HTTP requests received.",
			}),
			ingestFailedTotal: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "ingest_failed_total",
				Help: "Total number of failed ingestion operations.",
			}),
			ingestBatchDurationSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
				Name:    "ingest_batch_duration_seconds",
				Help:    "Duration of ingestion batch flushes.",
				Buckets: latencyBuckets(),
			}),
			cassandraWriteLatencySeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
				Name:    "cassandra_write_latency_seconds",
				Help:    "Latency of Cassandra write operations for ingestion batches.",
				Buckets: latencyBuckets(),
			}),
			ingestQueueOverflowTotal: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "ingest_queue_overflow_total",
				Help: "Total number of spans rejected because the ingestion queue was full or protected.",
			}),
		}

		prometheus.MustRegister(
			metricsSet.ingestRequestsTotal,
			metricsSet.ingestFailedTotal,
			metricsSet.ingestBatchDurationSeconds,
			metricsSet.cassandraWriteLatencySeconds,
			metricsSet.ingestQueueOverflowTotal,
		)
	})

	return metricsSet
}

func latencyBuckets() []float64 {
	return []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1}
}

func instrumentHTTPHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		getMetrics().ingestRequestsTotal.Inc()

		recorder := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(recorder, r)

		if recorder.statusCode >= http.StatusBadRequest {
			getMetrics().ingestFailedTotal.Inc()
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}
