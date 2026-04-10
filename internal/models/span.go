// Package models defines the core domain entities for Trace Forge.
// These structs map directly to Cassandra tables via CQL tags.
package models

import (
	"time"

	"github.com/google/uuid"
)

// Span represents a single span within a distributed trace.
// Maps to spans_by_trace table.
type Span struct {
	TraceID     uuid.UUID       `cql:"trace_id"`
	SpanID      uuid.UUID       `cql:"span_id"`
	ParentID    uuid.UUID       `cql:"parent_id"`
	ServiceName string          `cql:"service_name"`
	StartTime   time.Time       `cql:"start_time"`
	DurationMs  int64           `cql:"duration"`
	Tags        map[string]string `cql:"tags"`
}
