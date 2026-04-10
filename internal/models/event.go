// Package models defines the core domain entities for Trace Forge.
// These structs map directly to Cassandra tables via CQL tags.
package models

import (
	"time"

	"github.com/google/uuid"
)

// Event represents a session event (user action, error, or log).
// Maps to events_by_session table.
type Event struct {
	SessionID uuid.UUID         `cql:"session_id"`
	EventID   uuid.UUID         `cql:"event_id"`
	Timestamp time.Time         `cql:"ts"`
	Payload   []byte            `cql:"payload"`
	Tags      map[string]string `cql:"tags"`
}
