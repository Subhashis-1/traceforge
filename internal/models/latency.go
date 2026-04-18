package models

// LatencyMetric represents an aggregated latency data point for a minute bucket.
type LatencyMetric struct {
	Minute int     `json:"minute"`
	P95    float64 `json:"p95"`
	P99    float64 `json:"p99"`
}
