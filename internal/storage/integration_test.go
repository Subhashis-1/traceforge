// +build integration

package storage

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Subhashis-1/traceforge/internal/models"
)

var (
	cassandraContainer testcontainers.Container
	testSession        *gocql.Session
	testRepository     *CassandraRepository
)

// TestMain sets up and tears down the Cassandra container for all integration tests.
// Run with: go test -tags=integration ./internal/storage
func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Start Cassandra container
	container, err := startCassandraContainer(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start cassandra container: %v\n", err)
		os.Exit(1)
	}

	cassandraContainer = container

	// Get container host and port
	host, err := container.Host(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get container host: %v\n", err)
		if err := container.Terminate(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "failed to terminate container: %v\n", err)
		}
		os.Exit(1)
	}

	port, err := container.MappedPort(ctx, "9042/tcp")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get mapped port: %v\n", err)
		if err := container.Terminate(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "failed to terminate container: %v\n", err)
		}
		os.Exit(1)
	}

	// Create gocql session
	session, repo, err := setupSession(ctx, host, port.Port())
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to setup session: %v\n", err)
		if err := container.Terminate(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "failed to terminate container: %v\n", err)
		}
		os.Exit(1)
	}

	testSession = session
	testRepository = repo

	// Run tests
	code := m.Run()

	// Cleanup
	if testSession != nil {
		testSession.Close()
	}
	if err := container.Terminate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "failed to terminate container: %v\n", err)
	}

	os.Exit(code)
}

// startCassandraContainer starts a Cassandra 4.1 container and waits for it to be ready.
func startCassandraContainer(ctx context.Context) (testcontainers.Container, error) {
	req := testcontainers.ContainerRequest{
		Image:        "cassandra:4.1",
		ExposedPorts: []string{"9042/tcp"},
		WaitingFor: wait.ForAll(
			wait.ForLog("created default superuser role").WithStartupTimeout(60 * time.Second),
			wait.ForListeningPort("9042/tcp"),
		),
		Env: map[string]string{
			"CASSANDRA_CLUSTER_NAME": "traceforge-test",
		},
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})

	if err != nil {
		return nil, fmt.Errorf("start container: %w", err)
	}

	return container, nil
}

// setupSession creates a gocql session and applies the schema.
func setupSession(_ context.Context, host, port string) (*gocql.Session, *CassandraRepository, error) {
	// Build connection string
	addr := fmt.Sprintf("%s:%s", host, port)

	// Create cluster
	cluster := gocql.NewCluster(addr)
	cluster.Keyspace = "system"
	cluster.Consistency = gocql.Quorum
	cluster.Timeout = 10 * time.Second
	cluster.ConnectTimeout = 10 * time.Second

	// Create session to system keyspace for schema creation
	session, err := cluster.CreateSession()
	if err != nil {
		return nil, nil, fmt.Errorf("create system session: %w", err)
	}
	defer session.Close()

	// Create keyspace if not exists
	createKeyspaceQuery := `
		CREATE KEYSPACE IF NOT EXISTS traceforge
		WITH replication = {
			'class': 'SimpleStrategy',
			'replication_factor': '1'
		}
		AND durable_writes = true
	`
	if err := session.Query(createKeyspaceQuery).Exec(); err != nil {
		return nil, nil, fmt.Errorf("create keyspace: %w", err)
	}

	// Create tables
	tables := []string{
		// traces_by_service table
		`CREATE TABLE IF NOT EXISTS traceforge.traces_by_service (
			service_name text,
			date_bucket date,
			start_time timestamp,
			trace_id uuid,
			root_span_id uuid,
			duration bigint,
			status int,
			tags map<text, text>,
			PRIMARY KEY ((service_name, date_bucket), start_time, trace_id)
		) WITH default_time_to_live = 2592000
			AND CLUSTERING ORDER BY (start_time DESC, trace_id ASC)`,

		// spans_by_trace table
		`CREATE TABLE IF NOT EXISTS traceforge.spans_by_trace (
			trace_id uuid,
			span_id uuid,
			parent_id uuid,
			service_name text,
			start_time timestamp,
			duration bigint,
			tags map<text, text>,
			PRIMARY KEY (trace_id, span_id)
		) WITH CLUSTERING ORDER BY (span_id ASC)`,

		// events_by_session table
		`CREATE TABLE IF NOT EXISTS traceforge.events_by_session (
			session_id uuid,
			date_bucket date,
			ts timestamp,
			event_id uuid,
			payload blob,
			tags map<text, text>,
			PRIMARY KEY ((session_id, date_bucket), ts, event_id)
		) WITH default_time_to_live = 2592000
			AND CLUSTERING ORDER BY (ts ASC, event_id ASC)`,

		// trace_by_id table
		`CREATE TABLE IF NOT EXISTS traceforge.trace_by_id (
			trace_id uuid PRIMARY KEY,
			serialized blob
		)`,
	}

	// Execute each CREATE TABLE statement
	for _, tableStmt := range tables {
		if err := session.Query(tableStmt).Exec(); err != nil {
			return nil, nil, fmt.Errorf("create table: %w", err)
		}
	}

	// Create a new session with the traceforge keyspace
	cluster.Keyspace = "traceforge"
	appSession, err := cluster.CreateSession()
	if err != nil {
		return nil, nil, fmt.Errorf("create app session: %w", err)
	}

	// Create repository
	repo := &CassandraRepository{
		session: appSession,
	}

	return appSession, repo, nil
}

// TestWriteIdempotent verifies that CreateTrace is idempotent.
// Calling CreateTrace twice with the same trace should not create duplicates.
func TestWriteIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a trace with a fixed ID for reproducibility
	traceID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	trace := &models.Trace{
		TraceID:     traceID,
		ServiceName: "order-service",
		StartTime:   time.Now().UTC(),
		RootSpanID:  uuid.New(),
		DurationMs:  100,
		Status:      0,
		Tags: map[string]string{
			"env": "test",
		},
	}

	// First create
	err := testRepository.CreateTrace(ctx, trace)
	require.NoError(t, err, "first CreateTrace should succeed")

	// Sleep briefly to ensure write is committed
	time.Sleep(100 * time.Millisecond)

	// Second create with same trace (should be idempotent)
	err = testRepository.CreateTrace(ctx, trace)
	require.NoError(t, err, "second CreateTrace should succeed (idempotent)")

	// Query directly to verify only 1 row exists in traces_by_service
	bucket := models.DateBucket(trace.StartTime)
	query := testSession.Query(
		`SELECT trace_id FROM traces_by_service WHERE service_name = ? AND date_bucket = ? AND trace_id = ?`,
		trace.ServiceName, bucket, trace.TraceID,
	).WithContext(ctx)

	iter := query.Iter()
	defer iter.Close()

	rowCount := 0
	var rowTraceID uuid.UUID
	for iter.Scan(&rowTraceID) {
		rowCount++
	}

	require.NoError(t, iter.Close(), "iterator should close without error")
	require.Equal(t, 1, rowCount, "should have exactly 1 row for this trace (idempotent)")
	require.Equal(t, trace.TraceID, rowTraceID, "row should contain correct trace ID")
}

// TestReadLatency measures query performance for ListTraces.
// Target: sub-30ms for local Docker, but realistic for integration tests.
func TestReadLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Insert 10 random traces for "payment-service"
	serviceName := "payment-service"
	now := time.Now().UTC()
	startTime := now.Add(-1 * time.Hour)

	for i := 0; i < 10; i++ {
		trace := &models.Trace{
			TraceID:     uuid.New(),
			ServiceName: serviceName,
			StartTime:   startTime.Add(time.Duration(i*5) * time.Minute),
			RootSpanID:  uuid.New(),
			DurationMs:  int64(50 + i*10),
			Status:      0,
			Tags: map[string]string{
				"index": fmt.Sprintf("%d", i),
			},
		}

		err := testRepository.CreateTrace(ctx, trace)
		require.NoError(t, err, "CreateTrace should succeed for trace %d", i)
	}

	// Sleep to ensure writes are committed
	time.Sleep(200 * time.Millisecond)

	// Measure ListTraces latency
	start := time.Now()
	traces, err := testRepository.ListTraces(ctx, serviceName, startTime, now, 100)
	elapsed := time.Since(start)

	require.NoError(t, err, "ListTraces should succeed")
	require.Greater(t, len(traces), 0, "should return at least some traces")
	t.Logf("ListTraces returned %d traces in %v", len(traces), elapsed)

	// Threshold: 50ms for integration test (Docker overhead)
	// In production, target is <30ms for local Cassandra
	maxLatency := int64(50)
	require.LessOrEqual(
		t,
		elapsed.Milliseconds(),
		maxLatency,
		"ListTraces should complete within %dms (actual: %dms)",
		maxLatency,
		elapsed.Milliseconds(),
	)
}

// TestSpansAndEvents verifies span and event operations end-to-end.
func TestSpansAndEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	traceID := uuid.New()
	serviceName := "notification-service"
	now := time.Now().UTC()

	t.Run("spans_roundtrip", func(t *testing.T) {
		// Create 3 spans
		spans := make([]*models.Span, 3)
		for i := 0; i < 3; i++ {
			spans[i] = &models.Span{
				TraceID:     traceID,
				SpanID:      uuid.New(),
				ParentID:    uuid.Nil,
				ServiceName: serviceName,
				StartTime:   now.Add(time.Duration(i) * time.Millisecond),
				DurationMs:  int64(10 + i*5),
				Tags: map[string]string{
					"index": fmt.Sprintf("%d", i),
				},
			}

			err := testRepository.CreateSpan(ctx, spans[i])
			require.NoError(t, err, "CreateSpan should succeed for span %d", i)
		}

		// Sleep to ensure writes are committed
		time.Sleep(100 * time.Millisecond)

		// List spans for this trace
		retrievedSpans, err := testRepository.ListSpansByTrace(ctx, traceID)
		require.NoError(t, err, "ListSpansByTrace should succeed")
		require.Equal(t, len(spans), len(retrievedSpans), "should retrieve all spans")

		// Verify each span
		for i, retrieved := range retrievedSpans {
			require.Equal(t, spans[i].TraceID, retrieved.TraceID, "span %d trace ID should match", i)
			require.Equal(t, spans[i].SpanID, retrieved.SpanID, "span %d span ID should match", i)
			require.Equal(t, spans[i].ServiceName, retrieved.ServiceName, "span %d service name should match", i)
		}
	})

	t.Run("events_ordering", func(t *testing.T) {
		sessionID := uuid.New()
		eventCount := 5

		// Create events with specific timestamps to verify ordering
		events := make([]*models.Event, eventCount)
		baseTime := now
		for i := 0; i < eventCount; i++ {
			events[i] = &models.Event{
				SessionID: sessionID,
				EventID:   uuid.New(),
				Timestamp: baseTime.Add(time.Duration(i*100) * time.Millisecond),
				Payload:   []byte(fmt.Sprintf("event_%d", i)),
				Tags: map[string]string{
					"type": "user_action",
				},
			}

			err := testRepository.CreateEvent(ctx, events[i])
			require.NoError(t, err, "CreateEvent should succeed for event %d", i)
		}

		// Sleep to ensure writes are committed
		time.Sleep(100 * time.Millisecond)

		// List events for this session
		endTime := baseTime.Add(time.Duration((eventCount)*100) * time.Millisecond)
		retrievedEvents, err := testRepository.ListEventsBySession(ctx, sessionID, baseTime, endTime, 100)
		require.NoError(t, err, "ListEventsBySession should succeed")
		require.Equal(t, eventCount, len(retrievedEvents), "should retrieve all events")

		// Verify events are in chronological order (ascending by timestamp)
		for i, event := range retrievedEvents {
			require.Equal(t, events[i].SessionID, event.SessionID, "event %d session ID should match", i)
			require.Equal(t, events[i].Timestamp, event.Timestamp, "event %d timestamp should match", i)

			// Verify ordering: each event should be >= previous
			if i > 0 {
				require.True(
					t,
					event.Timestamp.After(retrievedEvents[i-1].Timestamp) || event.Timestamp.Equal(retrievedEvents[i-1].Timestamp),
					"event %d should be ordered after event %d", i, i-1,
				)
			}
		}
	})
}

// TestTraceBlobRoundtrip verifies blob storage and retrieval.
func TestTraceBlobRoundtrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	traceID := uuid.New()
	blobData := []byte("serialized_protobuf_trace_data_here")

	// Create blob
	err := testRepository.CreateTraceBlob(ctx, traceID, blobData)
	require.NoError(t, err, "CreateTraceBlob should succeed")

	// Sleep to ensure write is committed
	time.Sleep(100 * time.Millisecond)

	// Retrieve blob
	retrievedBlob, err := testRepository.GetTraceBlob(ctx, traceID)
	require.NoError(t, err, "GetTraceBlob should succeed")
	require.Equal(t, blobData, retrievedBlob, "retrieved blob should match stored blob")
}

// TestMultiServiceQuery verifies that ListTraces filters correctly by service name.
func TestMultiServiceQuery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	now := time.Now().UTC()
	startTime := now.Add(-2 * time.Hour)

	// Insert traces for two different services
	services := []string{"user-service", "product-service"}
	tracesPerService := 5

	for svc := range services {
		for i := 0; i < tracesPerService; i++ {
			trace := &models.Trace{
				TraceID:     uuid.New(),
				ServiceName: services[svc],
				StartTime:   startTime.Add(time.Duration(i) * time.Minute),
				RootSpanID:  uuid.New(),
				DurationMs:  int64(50),
				Status:      0,
				Tags: map[string]string{
					"service": services[svc],
				},
			}

			err := testRepository.CreateTrace(ctx, trace)
			require.NoError(t, err, "CreateTrace should succeed")
		}
	}

	// Sleep to ensure writes are committed
	time.Sleep(200 * time.Millisecond)

	// Query for each service and verify service filter
	for _, serviceName := range services {
		traces, err := testRepository.ListTraces(ctx, serviceName, startTime, now, 100)
		require.NoError(t, err, "ListTraces should succeed for service %s", serviceName)

		// Verify all traces belong to requested service
		for _, trace := range traces {
			require.Equal(t, serviceName, trace.ServiceName, "trace should belong to requested service")
		}

		t.Logf("Service %s returned %d traces", serviceName, len(traces))
	}
}
