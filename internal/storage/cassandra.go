// Package storage provides the data access layer for Trace Forge.
package storage

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"

	"github.com/Subhashis-1/traceforge/internal/models"
)

// CassandraRepository implements Repository using a gocql session.
type CassandraRepository struct {
	session *gocql.Session
}

// NewCassandraRepository creates a Cassandra-backed repository.
func NewCassandraRepository(clusterHosts []string, keyspace string) (*CassandraRepository, error) {
	cluster := gocql.NewCluster(clusterHosts...)
	cluster.Keyspace = keyspace
	cluster.Consistency = gocql.Quorum
	cluster.Timeout = 5 * time.Second
	cluster.ConnectTimeout = 5 * time.Second
	cluster.NumConns = 4

	session, err := cluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("cassandra init: %w", err)
	}

	return &CassandraRepository{session: session}, nil
}

// Close closes the underlying Cassandra session.
func (r *CassandraRepository) Close() error {
	if r.session != nil {
		r.session.Close()
	}
	return nil
}

// CreateTrace inserts a trace into the service table and placeholder blob table.
func (r *CassandraRepository) CreateTrace(ctx context.Context, t *models.Trace) error {
	bucket := models.DateBucket(t.StartTime)
	batch := r.session.NewBatch(gocql.UnloggedBatch).WithContext(ctx)

	batch.Query(`
		INSERT INTO traces_by_service
		(service_name, date_bucket, start_time, trace_id, root_span_id, duration, status, tags)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ServiceName, bucket, t.StartTime, toGocqlUUID(t.TraceID), toGocqlUUID(t.RootSpanID),
		t.DurationMs, t.Status, t.Tags,
	)

	batch.Query(`
		INSERT INTO trace_by_id (trace_id, serialized)
		VALUES (?, ?)`,
		toGocqlUUID(t.TraceID), nil,
	)

	if err := r.session.ExecuteBatch(batch); err != nil {
		return fmt.Errorf("create trace: %w", err)
	}

	return nil
}

// GetTraceByID retrieves a trace by ID, using the blob table as the fast path.
func (r *CassandraRepository) GetTraceByID(ctx context.Context, id uuid.UUID) (*models.Trace, error) {
	var blob []byte
	err := r.session.Query(`
		SELECT serialized FROM trace_by_id WHERE trace_id = ? LIMIT 1`,
		toGocqlUUID(id),
	).WithContext(ctx).Scan(&blob)
	if err == nil && blob != nil {
		return &models.Trace{TraceID: id}, nil
	}
	if err != nil && err != gocql.ErrNotFound {
		return nil, fmt.Errorf("get trace by id: %w", err)
	}

	var t models.Trace
	var traceID gocql.UUID
	var rootSpanID gocql.UUID
	err = r.session.Query(`
		SELECT service_name, start_time, trace_id, root_span_id, duration, status, tags
		FROM traces_by_service
		WHERE trace_id = ? ALLOW FILTERING LIMIT 1`,
		toGocqlUUID(id),
	).WithContext(ctx).Scan(&t.ServiceName, &t.StartTime, &traceID, &rootSpanID, &t.DurationMs, &t.Status, &t.Tags)
	if err != nil {
		if err == gocql.ErrNotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get trace by id: %w", err)
	}
	t.TraceID = fromGocqlUUID(traceID)
	t.RootSpanID = fromGocqlUUID(rootSpanID)

	return &t, nil
}

// ListTraces returns traces for a service within the provided time window.
func (r *CassandraRepository) ListTraces(ctx context.Context, service string, start, end time.Time, limit int) ([]*models.Trace, error) {
	buckets := getDateBuckets(start, end)

	type traceResult struct {
		traces []*models.Trace
		err    error
	}

	results := make(chan traceResult, len(buckets))
	var wg sync.WaitGroup

	for _, bucket := range buckets {
		wg.Add(1)
		go func(bucket string) {
			defer wg.Done()

			iter := r.session.Query(`
				SELECT service_name, start_time, trace_id, root_span_id, duration, status, tags
				FROM traces_by_service
				WHERE service_name = ? AND date_bucket = ?
				      AND start_time >= ? AND start_time <= ?
				LIMIT ?`,
				service, bucket, start, end, limit,
			).WithContext(ctx).Iter()

			var traces []*models.Trace
			var (
				traceID    gocql.UUID
				rootSpanID gocql.UUID
				t          models.Trace
			)
			for iter.Scan(&t.ServiceName, &t.StartTime, &traceID, &rootSpanID, &t.DurationMs, &t.Status, &t.Tags) {
				t.TraceID = fromGocqlUUID(traceID)
				t.RootSpanID = fromGocqlUUID(rootSpanID)
				traceCopy := t
				traces = append(traces, &traceCopy)
			}

			if err := iter.Close(); err != nil {
				results <- traceResult{err: fmt.Errorf("list traces bucket %s: %w", bucket, err)}
				return
			}

			results <- traceResult{traces: traces}
		}(bucket)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var traces []*models.Trace
	for result := range results {
		if result.err != nil {
			return nil, fmt.Errorf("list traces: %w", result.err)
		}
		traces = append(traces, result.traces...)
	}

	sort.Slice(traces, func(i, j int) bool {
		return traces[i].StartTime.After(traces[j].StartTime)
	})

	if limit > 0 && len(traces) > limit {
		traces = traces[:limit]
	}

	return traces, nil
}

// CreateSpan inserts a span row for a trace.
func (r *CassandraRepository) CreateSpan(ctx context.Context, s *models.Span) error {
	err := r.session.Query(`
		INSERT INTO spans_by_trace
		(trace_id, span_id, parent_id, service_name, start_time, duration, tags)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		toGocqlUUID(s.TraceID), toGocqlUUID(s.SpanID), toGocqlUUID(s.ParentID), s.ServiceName,
		s.StartTime, s.DurationMs, s.Tags,
	).WithContext(ctx).Exec()
	if err != nil {
		return fmt.Errorf("create span: %w", err)
	}

	return nil
}

// ListSpansByTrace retrieves all spans for a given trace.
func (r *CassandraRepository) ListSpansByTrace(ctx context.Context, traceID uuid.UUID) ([]*models.Span, error) {
	iter := r.session.Query(`
		SELECT trace_id, span_id, parent_id, service_name, start_time, duration, tags
		FROM spans_by_trace
		WHERE trace_id = ?`,
		toGocqlUUID(traceID),
	).WithContext(ctx).Iter()

	var spans []*models.Span
	var (
		traceKey gocql.UUID
		spanID   gocql.UUID
		parentID gocql.UUID
		s        models.Span
	)
	for iter.Scan(&traceKey, &spanID, &parentID, &s.ServiceName, &s.StartTime, &s.DurationMs, &s.Tags) {
		s.TraceID = fromGocqlUUID(traceKey)
		s.SpanID = fromGocqlUUID(spanID)
		s.ParentID = fromGocqlUUID(parentID)
		spanCopy := s
		spans = append(spans, &spanCopy)
	}

	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("list spans by trace: %w", err)
	}

	return spans, nil
}

// CreateEvent inserts a UI session event.
func (r *CassandraRepository) CreateEvent(ctx context.Context, e *models.Event) error {
	bucket := models.DateBucket(e.Timestamp)
	err := r.session.Query(`
		INSERT INTO events_by_session
		(session_id, date_bucket, ts, event_id, payload, tags)
		VALUES (?, ?, ?, ?, ?, ?)`,
		toGocqlUUID(e.SessionID), bucket, e.Timestamp, toGocqlUUID(e.EventID), e.Payload, e.Tags,
	).WithContext(ctx).Exec()
	if err != nil {
		return fmt.Errorf("create event: %w", err)
	}

	return nil
}

// ListEventsBySession returns events for a session and time range.
func (r *CassandraRepository) ListEventsBySession(ctx context.Context, sessionID uuid.UUID, start, end time.Time, limit int) ([]*models.Event, error) {
	buckets := getDateBuckets(start, end)

	type eventResult struct {
		events []*models.Event
		err    error
	}

	results := make(chan eventResult, len(buckets))
	var wg sync.WaitGroup

	for _, bucket := range buckets {
		wg.Add(1)
		go func(bucket string) {
			defer wg.Done()

			iter := r.session.Query(`
				SELECT session_id, event_id, ts, payload, tags
				FROM events_by_session
				WHERE session_id = ? AND date_bucket = ?
				      AND ts >= ? AND ts <= ?
				LIMIT ?`,
				toGocqlUUID(sessionID), bucket, start, end, limit,
			).WithContext(ctx).Iter()

			var events []*models.Event
			var (
				sessionUUID gocql.UUID
				eventUUID   gocql.UUID
				e           models.Event
			)
			for iter.Scan(&sessionUUID, &eventUUID, &e.Timestamp, &e.Payload, &e.Tags) {
				e.SessionID = fromGocqlUUID(sessionUUID)
				e.EventID = fromGocqlUUID(eventUUID)
				eventCopy := e
				events = append(events, &eventCopy)
			}

			if err := iter.Close(); err != nil {
				results <- eventResult{err: fmt.Errorf("list events bucket %s: %w", bucket, err)}
				return
			}

			results <- eventResult{events: events}
		}(bucket)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var events []*models.Event
	for result := range results {
		if result.err != nil {
			return nil, fmt.Errorf("list events by session: %w", result.err)
		}
		events = append(events, result.events...)
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp.Before(events[j].Timestamp)
	})

	if limit > 0 && len(events) > limit {
		events = events[:limit]
	}

	return events, nil
}

// CreateTraceBlob stores a pre-serialized trace blob.
func (r *CassandraRepository) CreateTraceBlob(ctx context.Context, id uuid.UUID, blob []byte) error {
	err := r.session.Query(`
		INSERT INTO trace_by_id (trace_id, serialized)
		VALUES (?, ?)`,
		toGocqlUUID(id), blob,
	).WithContext(ctx).Exec()
	if err != nil {
		return fmt.Errorf("create trace blob: %w", err)
	}

	return nil
}

// GetTraceBlob retrieves a serialized trace blob.
func (r *CassandraRepository) GetTraceBlob(ctx context.Context, id uuid.UUID) ([]byte, error) {
	var data []byte
	err := r.session.Query(`
		SELECT serialized FROM trace_by_id WHERE trace_id = ?`,
		toGocqlUUID(id),
	).WithContext(ctx).Scan(&data)
	if err != nil {
		if err == gocql.ErrNotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get trace blob: %w", err)
	}

	return data, nil
}

func getDateBuckets(start, end time.Time) []string {
	startDate := time.Date(start.UTC().Year(), start.UTC().Month(), start.UTC().Day(), 0, 0, 0, 0, time.UTC)
	endDate := time.Date(end.UTC().Year(), end.UTC().Month(), end.UTC().Day(), 0, 0, 0, 0, time.UTC)

	var buckets []string
	for current := startDate; !current.After(endDate); current = current.AddDate(0, 0, 1) {
		buckets = append(buckets, models.DateBucket(current))
	}

	return buckets
}

// getDateBuckets returns all date buckets touched by the time range.
func (r *CassandraRepository) getDateBuckets(start, end time.Time) []string {
	return getDateBuckets(start, end)
}

func toGocqlUUID(id uuid.UUID) gocql.UUID {
	var value gocql.UUID
	copy(value[:], id[:])
	return value
}

func fromGocqlUUID(id gocql.UUID) uuid.UUID {
	var value uuid.UUID
	copy(value[:], id[:])
	return value
}

var _ Repository = (*CassandraRepository)(nil)
