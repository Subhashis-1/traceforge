// Package storage provides the data access layer for Trace Forge.
package storage

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Subhashis-1/traceforge/internal/models"
)

// MockRepository is a simple mock implementation of Repository for testing.
// It stores all data in memory and does not persist anything.
type MockRepository struct {
	mu sync.RWMutex

	// Storage
	traces          map[uuid.UUID]*models.Trace
	spans           map[uuid.UUID][]*models.Span
	events          map[uuid.UUID][]*models.Event
	traceBlobs      map[uuid.UUID][]byte
	sessionTraceMap map[uuid.UUID]models.SessionMap

	// Call tracking for assertions
	CreateTraceCallCount       int
	GetTraceByIDCallCount      int
	ListTracesCallCount        int
	CreateSpanCallCount        int
	CreateSpanBatchCount       int
	ListSpansByTraceCount      int
	CreateEventCallCount       int
	CreateSessionEventCount    int
	ListEventsBySessionCount   int
	GetTraceIDBySessionCount   int
	CreateSessionTraceMapCount int
	CreateTraceBlobCount       int
	GetTraceBlobCount          int

	// Optional error injection
	ErrorOnCreateTrace           error
	ErrorOnGetTraceByID          error
	ErrorOnListTraces            error
	ErrorOnCreateSpan            error
	ErrorOnCreateSpanBatch       error
	ErrorOnCreateSessionEvent    error
	ErrorOnGetTraceIDBySession   error
	ErrorOnCreateSessionTraceMap error
}

// NewMockRepository creates a new MockRepository instance.
func NewMockRepository() *MockRepository {
	return &MockRepository{
		traces:          make(map[uuid.UUID]*models.Trace),
		spans:           make(map[uuid.UUID][]*models.Span),
		events:          make(map[uuid.UUID][]*models.Event),
		traceBlobs:      make(map[uuid.UUID][]byte),
		sessionTraceMap: make(map[uuid.UUID]models.SessionMap),
	}
}

// CreateTrace stores a trace in memory.
func (m *MockRepository) CreateTrace(_ context.Context, t *models.Trace) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CreateTraceCallCount++

	if m.ErrorOnCreateTrace != nil {
		return m.ErrorOnCreateTrace
	}

	m.traces[t.TraceID] = t
	return nil
}

// GetTraceByID retrieves a trace by ID from memory.
func (m *MockRepository) GetTraceByID(_ context.Context, id uuid.UUID) (*models.Trace, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	m.GetTraceByIDCallCount++

	if m.ErrorOnGetTraceByID != nil {
		return nil, m.ErrorOnGetTraceByID
	}

	trace, exists := m.traces[id]
	if !exists {
		return nil, ErrNotFound
	}

	return trace, nil
}

// ListTraces returns all traces for a service (in-memory simulation).
func (m *MockRepository) ListTraces(_ context.Context, service string, start, end time.Time, limit int, pagingState []byte) ([]*models.Trace, []byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	m.ListTracesCallCount++

	if m.ErrorOnListTraces != nil {
		return nil, nil, m.ErrorOnListTraces
	}

	_ = pagingState // mark as intentionally unused

	var results []*models.Trace
	for _, trace := range m.traces {
		if trace.ServiceName == service &&
			!trace.StartTime.Before(start) &&
			!trace.StartTime.After(end) {
			results = append(results, trace)
		}
	}

	// Sort by start_time descending
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].StartTime.After(results[i].StartTime) {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	// Apply limit
	if len(results) > limit {
		results = results[:limit]
	}

	// For the mock, we do not support real paging, so always return nil for next cursor
	return results, nil, nil
}

// CreateSpan stores a span in memory.
func (m *MockRepository) CreateSpan(_ context.Context, s *models.Span) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CreateSpanCallCount++

	if m.ErrorOnCreateSpan != nil {
		return m.ErrorOnCreateSpan
	}

	m.spans[s.TraceID] = append(m.spans[s.TraceID], s)
	return nil
}

// CreateSpanBatch stores spans in memory.
func (m *MockRepository) CreateSpanBatch(_ context.Context, spans []*models.Span) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CreateSpanBatchCount++

	if m.ErrorOnCreateSpanBatch != nil {
		return m.ErrorOnCreateSpanBatch
	}

	for _, span := range spans {
		if span == nil {
			continue
		}
		m.spans[span.TraceID] = append(m.spans[span.TraceID], span)
	}

	return nil
}

// ListSpansByTrace retrieves all spans for a trace from memory.
func (m *MockRepository) ListSpansByTrace(_ context.Context, traceID uuid.UUID) ([]*models.Span, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	m.ListSpansByTraceCount++

	spans, exists := m.spans[traceID]
	if !exists {
		return []*models.Span{}, nil
	}

	return spans, nil
}

// CreateEvent stores an event in memory.
func (m *MockRepository) CreateEvent(_ context.Context, e *models.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CreateEventCallCount++

	m.events[e.SessionID] = append(m.events[e.SessionID], e)
	return nil
}

// CreateSessionEvent stores a session event and trace mapping in memory.
func (m *MockRepository) CreateSessionEvent(_ context.Context, ev *models.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CreateSessionEventCount++

	if m.ErrorOnCreateSessionEvent != nil {
		return m.ErrorOnCreateSessionEvent
	}

	m.events[ev.SessionID] = append(m.events[ev.SessionID], ev)
	m.sessionTraceMap[ev.SessionID] = models.SessionMap{
		SessionID: ev.SessionID,
		TraceID:   ev.TraceID,
		CreatedAt: ev.Timestamp,
	}
	return nil
}

// ListEventsBySession retrieves all events for a session from memory.
func (m *MockRepository) ListEventsBySession(_ context.Context, sessionID uuid.UUID, start, end time.Time, limit int) ([]*models.Event, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	m.ListEventsBySessionCount++

	events, exists := m.events[sessionID]
	if !exists {
		return []*models.Event{}, nil
	}

	var results []*models.Event
	for _, event := range events {
		if !event.Timestamp.Before(start) && !event.Timestamp.After(end) {
			results = append(results, event)
		}
	}

	// Sort by timestamp ascending
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Timestamp.Before(results[i].Timestamp) {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	// Apply limit
	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// GetTraceIDBySession returns the trace ID mapped to a session.
func (m *MockRepository) GetTraceIDBySession(_ context.Context, sessionID uuid.UUID) (uuid.UUID, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	m.GetTraceIDBySessionCount++

	if m.ErrorOnGetTraceIDBySession != nil {
		return uuid.Nil, m.ErrorOnGetTraceIDBySession
	}

	sessionMap, exists := m.sessionTraceMap[sessionID]
	if !exists {
		return uuid.Nil, ErrNotFound
	}

	return sessionMap.TraceID, nil
}

// CreateSessionTraceMap stores a direct session-to-trace mapping in memory.
func (m *MockRepository) CreateSessionTraceMap(_ context.Context, sessionID, traceID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CreateSessionTraceMapCount++

	if m.ErrorOnCreateSessionTraceMap != nil {
		return m.ErrorOnCreateSessionTraceMap
	}

	m.sessionTraceMap[sessionID] = models.SessionMap{
		SessionID: sessionID,
		TraceID:   traceID,
		CreatedAt: time.Now().UTC(),
	}

	return nil
}

// CreateTraceBlob stores a trace blob in memory.
func (m *MockRepository) CreateTraceBlob(_ context.Context, id uuid.UUID, blob []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CreateTraceBlobCount++

	m.traceBlobs[id] = blob
	return nil
}

// GetTraceBlob retrieves a trace blob from memory.
func (m *MockRepository) GetTraceBlob(_ context.Context, id uuid.UUID) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	m.GetTraceBlobCount++

	blob, exists := m.traceBlobs[id]
	if !exists {
		return nil, ErrNotFound
	}

	return blob, nil
}

// HealthCheck performs a mock health check (always succeeds).
func (m *MockRepository) HealthCheck(_ context.Context) error {
	return nil
}

// SearchTraces returns traces matching the provided query.
func (m *MockRepository) SearchTraces(_ context.Context, q *models.Query, limit int) ([]*models.Trace, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	results := make([]*models.Trace, 0, len(m.traces))
	for _, trace := range m.traces {
		if q != nil && q.Service != "" && trace.ServiceName != q.Service {
			continue
		}
		results = append(results, trace)
	}

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// Reset clears all data and call counts (useful for test cleanup).
func (m *MockRepository) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.traces = make(map[uuid.UUID]*models.Trace)
	m.spans = make(map[uuid.UUID][]*models.Span)
	m.events = make(map[uuid.UUID][]*models.Event)
	m.traceBlobs = make(map[uuid.UUID][]byte)

	m.CreateTraceCallCount = 0
	m.GetTraceByIDCallCount = 0
	m.ListTracesCallCount = 0
	m.CreateSpanCallCount = 0
	m.CreateSpanBatchCount = 0
	m.ListSpansByTraceCount = 0
	m.CreateEventCallCount = 0
	m.ListEventsBySessionCount = 0
	m.CreateSessionTraceMapCount = 0
	m.CreateTraceBlobCount = 0
	m.GetTraceBlobCount = 0
}

// Compile-time assertion to ensure MockRepository implements Repository.
var _ Repository = (*MockRepository)(nil)
