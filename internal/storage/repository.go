// Package storage provides the data access layer for Trace Forge.
// It abstracts all Cassandra reads and writes behind a clean interface.
package storage

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Subhashis-1/traceforge/internal/models"
)

// Repository abstracts all Cassandra reads/writes for Trace Forge.
// This interface enables testability through mocking and provides a clean
// separation between business logic and data access concerns.
type Repository interface {
	// Trace CRUD operations

	// CreateTrace inserts a new trace into traces_by_service table.
	// The trace is also written to trace_by_id for fast direct lookup.
	CreateTrace(ctx context.Context, t *models.Trace) error

	// GetTraceByID retrieves a trace by its unique ID from trace_by_id table.
	// Returns nil, ErrNotFound if the trace does not exist.
	GetTraceByID(ctx context.Context, id uuid.UUID) (*models.Trace, error)

	// ListTraces returns traces for a specific service within a time range.
	// Supports cursor-based pagination using Cassandra paging_state.
	// Returns traces, next paging state ([]byte, nil if no more pages), and error.
	ListTraces(ctx context.Context, service string, start, end time.Time, limit int, pagingState []byte) ([]*models.Trace, []byte, error)

	// Span CRUD operations

	// CreateSpan inserts a new span into spans_by_trace table.
	// Spans are always created as part of a trace and share the same partition.
	CreateSpan(ctx context.Context, s *models.Span) error

	// CreateSpanBatch inserts multiple spans in a single batch write.
	CreateSpanBatch(ctx context.Context, spans []*models.Span) error

	// ListSpansByTrace retrieves all spans for a given trace ID.
	// Spans are returned in order by span_id (clustering order).
	ListSpansByTrace(ctx context.Context, traceID uuid.UUID) ([]*models.Span, error)

	// Event CRUD operations

	// CreateEvent inserts a new session event into events_by_session table.
	// Events include a payload (blob) for flexible schema-less data storage.
	CreateEvent(ctx context.Context, e *models.Event) error

	// CreateSessionEvent stores a UI event that belongs to a session.
	// The Event struct already contains SessionID, TraceID, etc.
	CreateSessionEvent(ctx context.Context, ev *models.Event) error

	// ListEventsBySession returns events for a specific session within a time range.
	// Results are ordered chronologically by timestamp (ascending).
	ListEventsBySession(ctx context.Context, sessionID uuid.UUID, start, end time.Time, limit int) ([]*models.Event, error)

	// GetTraceIDBySession returns the trace ID that is mapped to a given session.
	// If no mapping exists, return uuid.Nil and an error.
	GetTraceIDBySession(ctx context.Context, sessionID uuid.UUID) (uuid.UUID, error)

	// CreateSessionTraceMap stores a direct session-to-trace correlation.
	CreateSessionTraceMap(ctx context.Context, sessionID, traceID uuid.UUID) error

	// Optional fast trace lookup (materialized blob storage)

	// CreateTraceBlob stores a pre-serialized trace (protobuf/JSON) for fast direct access.
	// This is an optimization to avoid joining spans_by_trace when fetching complete traces.
	CreateTraceBlob(ctx context.Context, id uuid.UUID, blob []byte) error

	// GetTraceBlob retrieves a pre-serialized trace by ID.
	// Returns nil, ErrNotFound if the trace does not exist.
	GetTraceBlob(ctx context.Context, id uuid.UUID) ([]byte, error)

	// HealthCheck performs a lightweight connectivity check.
	// Returns nil if Cassandra is reachable, error otherwise.
	HealthCheck(ctx context.Context) error

	// SearchTraces searches traces using a DSL query.
	// Returns matching traces based on the query filters.
	SearchTraces(ctx context.Context, q interface{}, limit int) ([]*models.Trace, error)
}
