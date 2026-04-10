// Package models defines the core domain entities for Trace Forge.
// These structs map directly to Cassandra tables via CQL tags.
package models

import (
	"time"

	"github.com/google/uuid"
)

// Trace represents a distributed trace containing multiple spans.
// Maps to traces_by_service and trace_by_id tables.
type Trace struct {
	TraceID     uuid.UUID       `cql:"trace_id" json:"trace_id"`
	ServiceName string          `cql:"service_name" json:"service_name"`
	StartTime   time.Time       `cql:"start_time" json:"start_time"`
	RootSpanID  uuid.UUID       `cql:"root_span_id" json:"root_span_id"`
	DurationMs  int64           `cql:"duration" json:"duration_ms"`
	Status      int             `cql:"status" json:"status"`
	Tags        map[string]string `cql:"tags" json:"tags"`
}

// DateBucket returns the UTC date bucket (YYYY-MM-DD) for a given time.
// Used for Cassandra partition key bucketing to prevent hot partitions.
func DateBucket(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}
