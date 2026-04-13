// Package models defines the core domain entities for Trace Forge.
// These structs map directly to Cassandra tables via CQL tags.
package models

import (
	"time"

	"github.com/google/uuid"
)

// SessionMap represents a mapping between a UI session ID and a backend trace ID.
// Maps to session_trace_map table with 30-day TTL.
type SessionMap struct {
	SessionID uuid.UUID `cql:"session_id" json:"session_id"`
	TraceID   uuid.UUID `cql:"trace_id" json:"trace_id"`
	CreatedAt time.Time `cql:"created_at" json:"created_at"`
}
