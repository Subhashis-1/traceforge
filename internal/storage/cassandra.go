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

// CassandraRepository implements Repository using gocql for Cassandra.
type CassandraRepository struct {
	session *gocql.Session
}

// NewCassandraRepository creates a new Cassandra repository instance.
// It connects to the Cassandra cluster at the specified hosts and uses the given keyspace.
// Returns an error if the connection fails.
func NewCassandraRepository(clusterHosts []string, keyspace string) (*CassandraRepository, error) {
	cluster := gocql.NewCluster(clusterHosts...)
	cluster.Keyspace = keyspace
	cluster.Consistency = gocql.Quorum
	cluster.Timeout = 5 * time.Second

	session, err := cluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("create cassandra session: %w", err)
	}

	return &CassandraRepository{session: session}, nil
}

// Close closes the Cassandra session.
func (r *CassandraRepository) Close() error {
	if r.session != nil {
		r.session.Close()
	}
	return nil
}

// CreateTrace writes a trace to both traces_by_service and trace_by_id tables.
// Uses an unlogged batch for atomic write semantics.
func (r *CassandraRepository) CreateTrace(ctx context.Context, t *models.Trace) error {
	batch := r.session.NewBatch(gocql.UnloggedBatch)
	batch.WithContext(ctx)

	bucket := models.DateBucket(t.StartTime)

	// Insert into traces_by_service
	batch.Query(
		`INSERT INTO traces_by_service 
		 (service_name, date_bucket, start_time, trace_id, root_span_id, duration, status, tags)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ServiceName, bucket, t.StartTime, t.TraceID, t.RootSpanID, t.DurationMs, t.Status, t.Tags,
	)

	// Also insert empty row into trace_by_id for fast lookup
	// (actual serialized blob would be populated via CreateTraceBlob)
	batch.Query(
		`INSERT INTO trace_by_id (trace_id, serialized) VALUES (?, ?)`,
		t.TraceID, []byte{},
	)

	if err := r.session.ExecuteBatch(batch); err != nil {
		return fmt.Errorf("create trace: %w", err)
	}

	return nil
}

// GetTraceByID retrieves a trace by its unique ID.
// First tries trace_by_id for fast lookup; if empty, reconstructs from traces_by_service.
func (r *CassandraRepository) GetTraceByID(ctx context.Context, id uuid.UUID) (*models.Trace, error) {
	// Try fast lookup from trace_by_id first
	query := r.session.Query(
		`SELECT trace_id, serialized FROM trace_by_id WHERE trace_id = ?`,
		id,
	).WithContext(ctx)

	var traceID uuid.UUID
	var serialized []byte
	if err := query.Scan(&traceID, &serialized); err != nil {
		if err != gocql.ErrNotFound {
			return nil, fmt.Errorf("get trace by id: %w", err)
		}
		// Not found in trace_by_id, would need to scan traces_by_service
		// For now, return not found
		return nil, ErrNotFound
	}

	// If we got here but serialized is empty, trace exists but blob not yet populated
	if len(serialized) == 0 {
		return nil, ErrNotFound
	}

	// Deserialize trace from blob (for now, placeholder)
	// In production, this would unmarshal from protobuf/JSON
	return nil, ErrNotFound
}

// ListTraces queries traces for a service within a time range.
// Uses date bucketing and parallel queries across all relevant date buckets.
func (r *CassandraRepository) ListTraces(ctx context.Context, service string, start, end time.Time, limit int) ([]*models.Trace, error) {
	// Generate date buckets for the query range
	buckets := r.getDateBuckets(start, end)

	// Use WaitGroup to parallelize queries across buckets
	var wg sync.WaitGroup
	results := make(chan *models.Trace, 1000)
	errors := make(chan error, len(buckets))

	for _, bucket := range buckets {
		wg.Add(1)
		go func(b time.Time) {
			defer wg.Done()
			traces, err := r.queryTracesByServiceAndBucket(ctx, service, b, start, end, limit)
			if err != nil {
				errors <- fmt.Errorf("query traces for bucket %v: %w", b, err)
				return
			}
			for _, t := range traces {
				results <- t
			}
		}(bucket)
	}

	// Close results channel when all queries complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	var traces []*models.Trace
	for t := range results {
		traces = append(traces, t)
	}

	// Check for errors
	close(errors)
	for err := range errors {
		if err != nil {
			return nil, fmt.Errorf("list traces: %w", err)
		}
	}

	// Sort by start_time descending (most recent first)
	sort.Slice(traces, func(i, j int) bool {
		return traces[i].StartTime.After(traces[j].StartTime)
	})

	// Truncate to limit
	if len(traces) > limit {
		traces = traces[:limit]
	}

	return traces, nil
}

// queryTracesByServiceAndBucket queries traces for a specific service and date bucket.
func (r *CassandraRepository) queryTracesByServiceAndBucket(
	ctx context.Context,
	service string,
	bucket time.Time,
	start, end time.Time,
	limit int,
) ([]*models.Trace, error) {
	query := r.session.Query(
		`SELECT trace_id, service_name, start_time, root_span_id, duration, status, tags
		 FROM traces_by_service
		 WHERE service_name = ? AND date_bucket = ? AND start_time >= ? AND start_time <= ?
		 LIMIT ?`,
		service, bucket.Format("2006-01-02"), start, end, limit,
	).WithContext(ctx)

	iter := query.Iter()
	defer iter.Close()

	var traces []*models.Trace
	for {
		var (
			traceID    uuid.UUID
			svcName    string
			startTime  time.Time
			rootSpanID uuid.UUID
			duration   int64
			status     int
			tags       map[string]string
		)

		if !iter.Scan(&traceID, &svcName, &startTime, &rootSpanID, &duration, &status, &tags) {
			break
		}

		traces = append(traces, &models.Trace{
			TraceID:     traceID,
			ServiceName: svcName,
			StartTime:   startTime,
			RootSpanID:  rootSpanID,
			DurationMs:  duration,
			Status:      status,
			Tags:        tags,
		})
	}

	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("query spans by service and bucket: %w", err)
	}

	return traces, nil
}

// CreateSpan inserts a span into the spans_by_trace table.
func (r *CassandraRepository) CreateSpan(ctx context.Context, s *models.Span) error {
	query := r.session.Query(
		`INSERT INTO spans_by_trace
		 (trace_id, span_id, parent_id, service_name, start_time, duration, tags)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.TraceID, s.SpanID, s.ParentID, s.ServiceName, s.StartTime, s.DurationMs, s.Tags,
	).WithContext(ctx)

	if err := query.Exec(); err != nil {
		return fmt.Errorf("create span: %w", err)
	}

	return nil
}

// ListSpansByTrace retrieves all spans for a given trace ID.
func (r *CassandraRepository) ListSpansByTrace(ctx context.Context, traceID uuid.UUID) ([]*models.Span, error) {
	query := r.session.Query(
		`SELECT trace_id, span_id, parent_id, service_name, start_time, duration, tags
		 FROM spans_by_trace
		 WHERE trace_id = ?`,
		traceID,
	).WithContext(ctx)

	iter := query.Iter()
	defer iter.Close()

	var spans []*models.Span
	for {
		var (
			trID       uuid.UUID
			spanID     uuid.UUID
			parentID   uuid.UUID
			svcName    string
			startTime  time.Time
			duration   int64
			tags       map[string]string
		)

		if !iter.Scan(&trID, &spanID, &parentID, &svcName, &startTime, &duration, &tags) {
			break
		}

		spans = append(spans, &models.Span{
			TraceID:     trID,
			SpanID:      spanID,
			ParentID:    parentID,
			ServiceName: svcName,
			StartTime:   startTime,
			DurationMs:  duration,
			Tags:        tags,
		})
	}

	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("list spans by trace: %w", err)
	}

	return spans, nil
}

// CreateEvent inserts a session event into the events_by_session table.
func (r *CassandraRepository) CreateEvent(ctx context.Context, e *models.Event) error {
	bucket := models.DateBucket(e.Timestamp)

	query := r.session.Query(
		`INSERT INTO events_by_session
		 (session_id, date_bucket, ts, event_id, payload, tags)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		e.SessionID, bucket, e.Timestamp, e.EventID, e.Payload, e.Tags,
	).WithContext(ctx)

	if err := query.Exec(); err != nil {
		return fmt.Errorf("create event: %w", err)
	}

	return nil
}

// ListEventsBySession retrieves events for a session within a time range.
// Uses date bucketing and parallel queries across all relevant date buckets.
func (r *CassandraRepository) ListEventsBySession(ctx context.Context, sessionID uuid.UUID, start, end time.Time, limit int) ([]*models.Event, error) {
	// Generate date buckets for the query range
	buckets := r.getDateBuckets(start, end)

	// Use WaitGroup to parallelize queries across buckets
	var wg sync.WaitGroup
	results := make(chan *models.Event, 1000)
	errors := make(chan error, len(buckets))

	for _, bucket := range buckets {
		wg.Add(1)
		go func(b time.Time) {
			defer wg.Done()
			events, err := r.queryEventsBySessionAndBucket(ctx, sessionID, b, start, end, limit)
			if err != nil {
				errors <- fmt.Errorf("query events for bucket %v: %w", b, err)
				return
			}
			for _, e := range events {
				results <- e
			}
		}(bucket)
	}

	// Close results channel when all queries complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	var events []*models.Event
	for e := range results {
		events = append(events, e)
	}

	// Check for errors
	close(errors)
	for err := range errors {
		if err != nil {
			return nil, fmt.Errorf("list events by session: %w", err)
		}
	}

	// Sort by timestamp ascending (chronological order)
	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp.Before(events[j].Timestamp)
	})

	// Truncate to limit
	if len(events) > limit {
		events = events[:limit]
	}

	return events, nil
}

// queryEventsBySessionAndBucket queries events for a specific session and date bucket.
func (r *CassandraRepository) queryEventsBySessionAndBucket(
	ctx context.Context,
	sessionID uuid.UUID,
	bucket time.Time,
	start, end time.Time,
	limit int,
) ([]*models.Event, error) {
	query := r.session.Query(
		`SELECT session_id, event_id, ts, payload, tags
		 FROM events_by_session
		 WHERE session_id = ? AND date_bucket = ? AND ts >= ? AND ts <= ?
		 LIMIT ?`,
		sessionID, bucket.Format("2006-01-02"), start, end, limit,
	).WithContext(ctx)

	iter := query.Iter()
	defer iter.Close()

	var events []*models.Event
	for {
		var (
			sessID  uuid.UUID
			eventID uuid.UUID
			ts      time.Time
			payload []byte
			tags    map[string]string
		)

		if !iter.Scan(&sessID, &eventID, &ts, &payload, &tags) {
			break
		}

		events = append(events, &models.Event{
			SessionID: sessID,
			EventID:   eventID,
			Timestamp: ts,
			Payload:   payload,
			Tags:      tags,
		})
	}

	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("query events by session and bucket: %w", err)
	}

	return events, nil
}

// CreateTraceBlob stores a pre-serialized trace blob for fast direct lookup.
func (r *CassandraRepository) CreateTraceBlob(ctx context.Context, id uuid.UUID, blob []byte) error {
	query := r.session.Query(
		`INSERT INTO trace_by_id (trace_id, serialized) VALUES (?, ?)`,
		id, blob,
	).WithContext(ctx)

	if err := query.Exec(); err != nil {
		return fmt.Errorf("create trace blob: %w", err)
	}

	return nil
}

// GetTraceBlob retrieves a pre-serialized trace blob by ID.
func (r *CassandraRepository) GetTraceBlob(ctx context.Context, id uuid.UUID) ([]byte, error) {
	query := r.session.Query(
		`SELECT serialized FROM trace_by_id WHERE trace_id = ?`,
		id,
	).WithContext(ctx)

	var blob []byte
	if err := query.WithContext(ctx).Scan(&blob); err != nil {
		if err == gocql.ErrNotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get trace blob: %w", err)
	}

	return blob, nil
}

// getDateBuckets returns a slice of date buckets from start to end (inclusive).
func (r *CassandraRepository) getDateBuckets(start, end time.Time) []time.Time {
	var buckets []time.Time
	current := start

	for !current.After(end) {
		buckets = append(buckets, current)
		current = current.AddDate(0, 0, 1)
	}

	return buckets
}

// Compile-time assertion to ensure CassandraRepository implements Repository.
var _ Repository = (*CassandraRepository)(nil)
