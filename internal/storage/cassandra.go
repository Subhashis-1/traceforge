// Package storage provides the data access layer for Trace Forge.
package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"

	"github.com/Subhashis-1/traceforge/internal/models"
)

const (
	queryInsertTrace = `
		INSERT INTO traces_by_service
		(service_name, date_bucket, start_time, trace_id, root_span_id, duration, status, tags)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	queryInsertTraceByID = `
		INSERT INTO trace_by_id
		(trace_id, service_name, date_bucket, start_time, root_span_id, duration, status, tags, serialized)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	querySelectTraceByID = `
		SELECT service_name, date_bucket, start_time, root_span_id, duration, status, tags, serialized
		FROM trace_by_id
		WHERE trace_id = ?`

	queryListTraces = `
		SELECT service_name, start_time, trace_id, root_span_id, duration, status, tags
		FROM traces_by_service
		WHERE service_name = ? AND date_bucket = ?
		AND start_time >= ? AND start_time <= ?`

	queryInsertSpan = `
		INSERT INTO spans_by_trace
		(trace_id, span_id, parent_id, service_name, start_time, duration, tags)
		VALUES (?, ?, ?, ?, ?, ?, ?)`

	queryListSpans = `
		SELECT trace_id, span_id, parent_id, service_name, start_time, duration, tags
		FROM spans_by_trace
		WHERE trace_id = ?`

	queryInsertEvent = `
		INSERT INTO events_by_session
		(session_id, date_bucket, ts, event_id, payload, tags)
		VALUES (?, ?, ?, ?, ?, ?)`

	queryListEvents = `
		SELECT session_id, event_id, ts, payload, tags
		FROM events_by_session
		WHERE session_id = ? AND date_bucket = ?
		AND ts >= ? AND ts <= ?
		LIMIT ?`

	queryUpdateTraceBlob = `
		INSERT INTO trace_by_id
		(trace_id, serialized)
		VALUES (?, ?)`

	querySelectTraceBlob = `
		SELECT serialized FROM trace_by_id WHERE trace_id = ?`

	queryHealth = `SELECT now() FROM system.local`
)

type tracePageCursor struct {
	BucketIndex int    `json:"bucket_index"`
	PageState   []byte `json:"page_state,omitempty"`
}

// CassandraRepository implements Repository using a gocql session.
type CassandraRepository struct {
	session *gocql.Session
}

// NewCassandraRepository creates a Cassandra-backed repository.
func NewCassandraRepository(clusterHosts []string, keyspace string) (*CassandraRepository, error) {
	cluster := gocql.NewCluster(clusterHosts...)
	cluster.Keyspace = keyspace
	cluster.Consistency = gocql.LocalQuorum
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

// CreateTrace inserts a trace into the service table and direct lookup table.
func (r *CassandraRepository) CreateTrace(ctx context.Context, t *models.Trace) error {
	bucket := models.DateBucket(t.StartTime)
	batch := r.session.NewBatch(gocql.UnloggedBatch).WithContext(ctx)
	batch.Cons = gocql.Quorum

	batch.Query(
		queryInsertTrace,
		t.ServiceName,
		bucket,
		t.StartTime.UTC(),
		toGocqlUUID(t.TraceID),
		toGocqlUUID(t.RootSpanID),
		t.DurationMs,
		t.Status,
		t.Tags,
	)
	batch.Query(
		queryInsertTraceByID,
		toGocqlUUID(t.TraceID),
		t.ServiceName,
		bucket,
		t.StartTime.UTC(),
		toGocqlUUID(t.RootSpanID),
		t.DurationMs,
		t.Status,
		t.Tags,
		nil,
	)

	if err := r.session.ExecuteBatch(batch); err != nil {
		return fmt.Errorf("create trace: %w", err)
	}

	return nil
}

// GetTraceByID retrieves a trace by ID using the direct lookup table.
func (r *CassandraRepository) GetTraceByID(ctx context.Context, id uuid.UUID) (*models.Trace, error) {
	var (
		serviceName string
		dateBucket  string
		startTime   time.Time
		rootSpanID  gocql.UUID
		durationMs  int64
		status      int
		tags        map[string]string
		blob        []byte
	)

	err := r.session.Query(querySelectTraceByID, toGocqlUUID(id)).
		WithContext(ctx).
		Consistency(gocql.LocalQuorum).
		Scan(&serviceName, &dateBucket, &startTime, &rootSpanID, &durationMs, &status, &tags, &blob)
	if err != nil {
		if err == gocql.ErrNotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get trace by id: %w", err)
	}

	trace := &models.Trace{
		TraceID:     id,
		ServiceName: serviceName,
		StartTime:   startTime.UTC(),
		RootSpanID:  fromGocqlUUID(rootSpanID),
		DurationMs:  durationMs,
		Status:      status,
		Tags:        tags,
	}

	if len(blob) > 0 {
		var decoded models.Trace
		if err := json.Unmarshal(blob, &decoded); err == nil {
			if decoded.TraceID == uuid.Nil {
				decoded.TraceID = id
			}
			return &decoded, nil
		}
	}

	_ = dateBucket
	return trace, nil
}

// ListTraces returns traces for a service within the provided time window, with cursor-based pagination.
func (r *CassandraRepository) ListTraces(ctx context.Context, service string, start, end time.Time, limit int, pagingState []byte) ([]*models.Trace, []byte, error) {
	if limit <= 0 {
		limit = 50
	}

	buckets := getDateBuckets(start, end)
	if len(buckets) == 0 {
		return nil, nil, nil
	}
	sort.Sort(sort.Reverse(sort.StringSlice(buckets)))

	cursor, err := decodeTraceCursor(pagingState)
	if err != nil {
		return nil, nil, fmt.Errorf("list traces: %w", err)
	}
	if cursor.BucketIndex < 0 || cursor.BucketIndex >= len(buckets) {
		cursor = tracePageCursor{}
	}

	traces := make([]*models.Trace, 0, limit)
	for bucketIndex := cursor.BucketIndex; bucketIndex < len(buckets) && len(traces) < limit; bucketIndex++ {
		bucketStart, bucketEnd := boundedBucketRange(buckets[bucketIndex], start, end)

		pageLimit := limit - len(traces)
		query := r.session.Query(queryListTraces, service, buckets[bucketIndex], bucketStart, bucketEnd).
			WithContext(ctx).
			Consistency(gocql.LocalQuorum).
			PageSize(pageLimit)
		if bucketIndex == cursor.BucketIndex && len(cursor.PageState) > 0 {
			query = query.PageState(cursor.PageState)
		}

		iter := query.Iter()
		var (
			traceID    gocql.UUID
			rootSpanID gocql.UUID
			serviceRow string
			startRow   time.Time
			durationMs int64
			status     int
			tags       map[string]string
		)
		for iter.Scan(&serviceRow, &startRow, &traceID, &rootSpanID, &durationMs, &status, &tags) {
			traces = append(traces, &models.Trace{
				TraceID:     fromGocqlUUID(traceID),
				ServiceName: serviceRow,
				StartTime:   startRow.UTC(),
				RootSpanID:  fromGocqlUUID(rootSpanID),
				DurationMs:  durationMs,
				Status:      status,
				Tags:        tags,
			})
			if len(traces) >= limit {
				break
			}
		}

		nextPageState := iter.PageState()
		if err := iter.Close(); err != nil {
			return nil, nil, fmt.Errorf("list traces: %w", err)
		}

		if len(nextPageState) > 0 {
			nextCursor, err := encodeTraceCursor(tracePageCursor{
				BucketIndex: bucketIndex,
				PageState:   nextPageState,
			})
			if err != nil {
				return nil, nil, fmt.Errorf("list traces: %w", err)
			}
			return traces, nextCursor, nil
		}

		cursor.PageState = nil
	}

	return traces, nil, nil
}

// CreateSpan inserts a span row for a trace.
func (r *CassandraRepository) CreateSpan(ctx context.Context, s *models.Span) error {
	err := r.session.Query(
		queryInsertSpan,
		toGocqlUUID(s.TraceID),
		toGocqlUUID(s.SpanID),
		toGocqlUUID(s.ParentID),
		s.ServiceName,
		s.StartTime.UTC(),
		s.DurationMs,
		s.Tags,
	).WithContext(ctx).Consistency(gocql.Quorum).Exec()
	if err != nil {
		return fmt.Errorf("create span: %w", err)
	}

	return nil
}

// CreateSpanBatch inserts multiple span rows using an unlogged batch.
func (r *CassandraRepository) CreateSpanBatch(ctx context.Context, spans []*models.Span) error {
	if len(spans) == 0 {
		return nil
	}

	batch := r.session.NewBatch(gocql.UnloggedBatch).WithContext(ctx)
	batch.Cons = gocql.Quorum
	for _, span := range spans {
		if span == nil {
			continue
		}
		batch.Query(
			queryInsertSpan,
			toGocqlUUID(span.TraceID),
			toGocqlUUID(span.SpanID),
			toGocqlUUID(span.ParentID),
			span.ServiceName,
			span.StartTime.UTC(),
			span.DurationMs,
			span.Tags,
		)
	}

	if len(batch.Entries) == 0 {
		return nil
	}

	if err := r.session.ExecuteBatch(batch); err != nil {
		return fmt.Errorf("create span batch: %w", err)
	}

	return nil
}

// ListSpansByTrace retrieves all spans for a given trace.
func (r *CassandraRepository) ListSpansByTrace(ctx context.Context, traceID uuid.UUID) ([]*models.Span, error) {
	iter := r.session.Query(queryListSpans, toGocqlUUID(traceID)).
		WithContext(ctx).
		Consistency(gocql.LocalQuorum).
		Iter()

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
		s.StartTime = s.StartTime.UTC()
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
	err := r.session.Query(
		queryInsertEvent,
		toGocqlUUID(e.SessionID),
		bucket,
		e.Timestamp.UTC(),
		toGocqlUUID(e.EventID),
		e.Payload,
		e.Tags,
	).WithContext(ctx).Consistency(gocql.Quorum).Exec()
	if err != nil {
		return fmt.Errorf("create event: %w", err)
	}

	return nil
}

// CreateSessionEvent stores a UI event that belongs to a session with trace correlation.
func (r *CassandraRepository) CreateSessionEvent(ctx context.Context, ev *models.Event) error {
	bucket := models.DateBucket(ev.Timestamp)
	err := r.session.Query(
		queryInsertEvent,
		toGocqlUUID(ev.SessionID),
		bucket,
		ev.Timestamp.UTC(),
		toGocqlUUID(ev.EventID),
		ev.Payload,
		ev.Tags,
	).WithContext(ctx).Consistency(gocql.Quorum).Exec()
	if err != nil {
		return fmt.Errorf("create session event: %w", err)
	}

	// Also create the session-trace mapping
	queryInsertSessionMap := `
		INSERT INTO session_trace_map
		(session_id, trace_id, created_at)
		VALUES (?, ?, ?)
		USING TTL 2592000`

	err = r.session.Query(
		queryInsertSessionMap,
		toGocqlUUID(ev.SessionID),
		toGocqlUUID(ev.TraceID),
		ev.Timestamp.UTC(),
	).WithContext(ctx).Consistency(gocql.Quorum).Exec()
	if err != nil {
		return fmt.Errorf("create session trace map: %w", err)
	}

	return nil
}

// ListEventsBySession returns events for a session and time range.
func (r *CassandraRepository) ListEventsBySession(ctx context.Context, sessionID uuid.UUID, start, end time.Time, limit int) ([]*models.Event, error) {
	if limit <= 0 {
		limit = 100
	}

	buckets := getDateBuckets(start, end)
	events := make([]*models.Event, 0, limit)
	for _, bucket := range buckets {
		bucketStart, bucketEnd := boundedBucketRange(bucket, start, end)
		iter := r.session.Query(
			queryListEvents,
			toGocqlUUID(sessionID),
			bucket,
			bucketStart,
			bucketEnd,
			limit,
		).WithContext(ctx).
			Consistency(gocql.LocalQuorum).
			PageSize(limit).
			Iter()

		var (
			sessionUUID gocql.UUID
			eventUUID   gocql.UUID
			e           models.Event
		)
		for iter.Scan(&sessionUUID, &eventUUID, &e.Timestamp, &e.Payload, &e.Tags) {
			e.SessionID = fromGocqlUUID(sessionUUID)
			e.EventID = fromGocqlUUID(eventUUID)
			e.Timestamp = e.Timestamp.UTC()
			eventCopy := e
			events = append(events, &eventCopy)
		}
		if err := iter.Close(); err != nil {
			return nil, fmt.Errorf("list events by session: %w", err)
		}
	}

	sort.Slice(events, func(i, j int) bool {
		if events[i].Timestamp.Equal(events[j].Timestamp) {
			return events[i].EventID.String() < events[j].EventID.String()
		}
		return events[i].Timestamp.Before(events[j].Timestamp)
	})

	if limit > 0 && len(events) > limit {
		events = events[:limit]
	}

	return events, nil
}

// GetTraceIDBySession returns the trace ID mapped to a session.
func (r *CassandraRepository) GetTraceIDBySession(ctx context.Context, sessionID uuid.UUID) (uuid.UUID, error) {
	querySelectSessionMap := `
		SELECT trace_id
		FROM session_trace_map
		WHERE session_id = ?`

	var traceID gocql.UUID
	err := r.session.Query(querySelectSessionMap, toGocqlUUID(sessionID)).
		WithContext(ctx).
		Consistency(gocql.LocalQuorum).
		Scan(&traceID)
	if err != nil {
		if err == gocql.ErrNotFound {
			return uuid.Nil, ErrNotFound
		}
		return uuid.Nil, fmt.Errorf("get trace ID by session: %w", err)
	}

	return fromGocqlUUID(traceID), nil
}

// CreateTraceBlob stores a pre-serialized trace blob.
func (r *CassandraRepository) CreateTraceBlob(ctx context.Context, id uuid.UUID, blob []byte) error {
	err := r.session.Query(queryUpdateTraceBlob, toGocqlUUID(id), blob).
		WithContext(ctx).
		Consistency(gocql.Quorum).
		Exec()
	if err != nil {
		return fmt.Errorf("create trace blob: %w", err)
	}

	return nil
}

// GetTraceBlob retrieves a serialized trace blob.
func (r *CassandraRepository) GetTraceBlob(ctx context.Context, id uuid.UUID) ([]byte, error) {
	var data []byte
	err := r.session.Query(querySelectTraceBlob, toGocqlUUID(id)).
		WithContext(ctx).
		Consistency(gocql.LocalQuorum).
		Scan(&data)
	if err != nil {
		if err == gocql.ErrNotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get trace blob: %w", err)
	}

	return data, nil
}

// HealthCheck performs a lightweight connectivity check on Cassandra.
func (r *CassandraRepository) HealthCheck(ctx context.Context) error {
	if err := r.session.Query(queryHealth).WithContext(ctx).Consistency(gocql.LocalQuorum).Exec(); err != nil {
		return fmt.Errorf("cassandra health check: %w", err)
	}
	return nil
}

func boundedBucketRange(bucket string, start, end time.Time) (time.Time, time.Time) {
	bucketStart, err := time.Parse("2006-01-02", bucket)
	if err != nil {
		return start.UTC(), end.UTC()
	}

	bucketStart = bucketStart.UTC()
	bucketEnd := bucketStart.Add(24*time.Hour - time.Nanosecond)
	if bucketStart.Before(start.UTC()) {
		bucketStart = start.UTC()
	}
	if bucketEnd.After(end.UTC()) {
		bucketEnd = end.UTC()
	}

	return bucketStart, bucketEnd
}

func decodeTraceCursor(raw []byte) (tracePageCursor, error) {
	if len(raw) == 0 {
		return tracePageCursor{}, nil
	}

	var cursor tracePageCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return tracePageCursor{}, fmt.Errorf("decode cursor: %w", err)
	}
	return cursor, nil
}

func encodeTraceCursor(cursor tracePageCursor) ([]byte, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return nil, fmt.Errorf("encode cursor: %w", err)
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
