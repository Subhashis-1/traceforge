//go:build integration
// +build integration

package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	integrationContainer testcontainers.Container
	integrationSession   *gocql.Session
	integrationRepo      *CassandraRepository
)

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "cassandra:4.1",
			ExposedPorts: []string{"9042/tcp"},
			WaitingFor:   wait.ForListeningPort("9042/tcp").WithStartupTimeout(4 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "start cassandra container: %v\n", err)
		os.Exit(1)
	}

	integrationContainer = container

	host, err := container.Host(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve cassandra host: %v\n", err)
		_ = container.Terminate(ctx)
		os.Exit(1)
	}

	port, err := container.MappedPort(ctx, "9042/tcp")
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve cassandra port: %v\n", err)
		_ = container.Terminate(ctx)
		os.Exit(1)
	}

	session, repo, err := createIntegrationRepository(ctx, host, port.Port())
	if err != nil {
		fmt.Fprintf(os.Stderr, "initialize integration repository: %v\n", err)
		_ = container.Terminate(ctx)
		os.Exit(1)
	}

	integrationSession = session
	integrationRepo = repo

	exitCode := func() int {
		defer func() {
			if integrationSession != nil {
				integrationSession.Close()
			}
			if integrationContainer != nil {
				_ = integrationContainer.Terminate(context.Background())
			}
		}()

		return m.Run()
	}()

	os.Exit(exitCode)
}

func createIntegrationRepository(ctx context.Context, host, port string) (*gocql.Session, *CassandraRepository, error) {
	cluster := gocql.NewCluster(fmt.Sprintf("%s:%s", host, port))
	cluster.Keyspace = "system"
	cluster.Consistency = gocql.Quorum
	cluster.Timeout = 10 * time.Second
	cluster.ConnectTimeout = 10 * time.Second

	var systemSession *gocql.Session
	var err error
	for attempt := 0; attempt < 45; attempt++ {
		systemSession, err = cluster.CreateSession()
		if err == nil {
			if pingErr := systemSession.Query(`SELECT release_version FROM system.local`).Exec(); pingErr == nil {
				break
			}
			systemSession.Close()
			err = fmt.Errorf("cassandra not ready for CQL yet")
		}

		select {
		case <-ctx.Done():
			return nil, nil, fmt.Errorf("create system session: %w", ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
	if err != nil {
		return nil, nil, fmt.Errorf("create system session: %w", err)
	}
	defer systemSession.Close()

	if err := ensureIntegrationKeyspace(systemSession); err != nil {
		return nil, nil, fmt.Errorf("create keyspace: %w", err)
	}

	cluster.Keyspace = "traceforge"
	appSession, err := cluster.CreateSession()
	if err != nil {
		return nil, nil, fmt.Errorf("create app session: %w", err)
	}

	schemaPath, err := integrationSchemaPath()
	if err != nil {
		appSession.Close()
		return nil, nil, fmt.Errorf("resolve schema path: %w", err)
	}

	if err := applySchema(appSession, schemaPath); err != nil {
		appSession.Close()
		return nil, nil, fmt.Errorf("apply schema: %w", err)
	}

	return appSession, &CassandraRepository{session: appSession}, nil
}

func ensureIntegrationKeyspace(session *gocql.Session) error {
	return session.Query(`
		CREATE KEYSPACE IF NOT EXISTS traceforge
		WITH replication = {
			'class': 'SimpleStrategy',
			'replication_factor': '1'
		}
		AND durable_writes = true`,
	).Exec()
}

func integrationSchemaPath() (string, error) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("runtime caller unavailable")
	}

	return filepath.Join(filepath.Dir(currentFile), "schema.cql"), nil
}

func applySchema(appSession *gocql.Session, schemaPath string) error {
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("read schema file: %w", err)
	}

	for _, statement := range splitCQLStatements(string(schemaBytes)) {
		stmt := strings.TrimSpace(statement)
		if stmt == "" {
			continue
		}

		if err := appSession.Query(stmt).Exec(); err != nil {
			if isIgnorableSchemaError(stmt, err) {
				continue
			}
			return fmt.Errorf("exec schema statement %q: %w", shortenStatement(stmt), err)
		}
	}

	return nil
}

func splitCQLStatements(content string) []string {
	lines := strings.Split(content, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		filtered = append(filtered, line)
	}

	return strings.Split(strings.Join(filtered, "\n"), ";")
}

func shortenStatement(stmt string) string {
	const maxLen = 80
	stmt = strings.Join(strings.Fields(stmt), " ")
	if len(stmt) <= maxLen {
		return stmt
	}
	return stmt[:maxLen] + "..."
}

func isIgnorableSchemaError(stmt string, err error) bool {
	stmt = strings.ToUpper(strings.TrimSpace(stmt))
	if strings.HasPrefix(stmt, "ALTER TABLE ") && strings.Contains(stmt, " ADD ") {
		errMsg := strings.ToLower(err.Error())
		return strings.Contains(errMsg, "conflicts with an existing column") ||
			strings.Contains(errMsg, "duplicate column") ||
			strings.Contains(errMsg, "invalid column name") ||
			strings.Contains(errMsg, "already exists")
	}

	return false
}

func TestWriteIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	trace := &models.Trace{
		TraceID:     uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ServiceName: "order",
		StartTime:   time.Date(2026, 4, 11, 9, 0, 0, 0, time.UTC),
		RootSpanID:  uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		DurationMs:  125,
		Status:      0,
		Tags:        map[string]string{"suite": "integration"},
	}

	require.NoError(t, integrationRepo.CreateTrace(ctx, trace))
	require.NoError(t, integrationRepo.CreateTrace(ctx, trace))

	got, err := integrationRepo.GetTraceByID(ctx, trace.TraceID)
	require.NoError(t, err)
	require.Equal(t, trace.TraceID, got.TraceID)
	require.Equal(t, trace.ServiceName, got.ServiceName)

	var count int
	err = integrationSession.Query(`
		SELECT COUNT(*) FROM trace_by_id WHERE trace_id = ?`,
		toGocqlUUID(trace.TraceID),
	).WithContext(ctx).Consistency(gocql.LocalQuorum).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestReadLatency(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	serviceName := "order"
	start := time.Now().UTC().Add(-15 * time.Minute).Truncate(time.Second)
	end := start.Add(10 * time.Minute)

	for i := 0; i < 10; i++ {
		trace := &models.Trace{
			TraceID:     uuid.New(),
			ServiceName: serviceName,
			StartTime:   start.Add(time.Duration(i) * time.Minute),
			RootSpanID:  uuid.New(),
			DurationMs:  int64(50 + i),
			Status:      0,
			Tags:        map[string]string{"index": fmt.Sprintf("%d", i)},
		}
		require.NoError(t, integrationRepo.CreateTrace(ctx, trace))
	}

	begin := time.Now()
	traces, nextCursor, err := integrationRepo.ListTraces(ctx, serviceName, start.Add(-time.Second), end, 100, nil)
	elapsed := time.Since(begin)

	require.NoError(t, err)
	require.Nil(t, nextCursor)
	require.GreaterOrEqual(t, len(traces), 10)
	require.Less(t, elapsed, 30*time.Millisecond)
}

func TestSearchTraces(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	serviceName := fmt.Sprintf("search-service-%s", uuid.NewString()[:8])
	trace := &models.Trace{
		TraceID:     uuid.New(),
		ServiceName: serviceName,
		StartTime:   time.Now().UTC().Truncate(time.Millisecond),
		RootSpanID:  uuid.New(),
		DurationMs:  150,
		Status:      0,
		Tags:        map[string]string{"suite": "search"},
	}

	require.NoError(t, integrationRepo.CreateTrace(ctx, trace))

	results, err := integrationRepo.SearchTraces(ctx, &models.Query{Service: serviceName}, 10)
	require.NoError(t, err)
	require.NotEmpty(t, results)

	found := false
	for _, result := range results {
		if result.TraceID == trace.TraceID {
			found = true
			require.Equal(t, serviceName, result.ServiceName)
			break
		}
	}

	require.True(t, found, "expected inserted trace to be returned by SearchTraces")
}

func TestSpansAndEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("span round trip", func(t *testing.T) {
		traceID := uuid.New()
		span := &models.Span{
			TraceID:     traceID,
			SpanID:      uuid.New(),
			ParentID:    uuid.Nil,
			ServiceName: "checkout",
			StartTime:   time.Now().UTC().Truncate(time.Millisecond),
			DurationMs:  42,
			Tags:        map[string]string{"kind": "root"},
		}

		require.NoError(t, integrationRepo.CreateSpan(ctx, span))

		spans, err := integrationRepo.ListSpansByTrace(ctx, traceID)
		require.NoError(t, err)
		require.Len(t, spans, 1)
		require.Equal(t, *span, *spans[0])
	})

	t.Run("event ordering", func(t *testing.T) {
		sessionID := uuid.New()
		baseTime := time.Now().UTC().Truncate(time.Millisecond)

		events := []*models.Event{
			{
				SessionID: sessionID,
				EventID:   uuid.New(),
				Timestamp: baseTime,
				Payload:   []byte("first"),
				Tags:      map[string]string{"step": "1"},
			},
			{
				SessionID: sessionID,
				EventID:   uuid.New(),
				Timestamp: baseTime.Add(100 * time.Millisecond),
				Payload:   []byte("second"),
				Tags:      map[string]string{"step": "2"},
			},
		}

		for _, event := range events {
			require.NoError(t, integrationRepo.CreateEvent(ctx, event))
		}

		got, err := integrationRepo.ListEventsBySession(ctx, sessionID, baseTime.Add(-time.Second), baseTime.Add(time.Second), 100)
		require.NoError(t, err)
		require.Len(t, got, len(events))
		require.Equal(t, events[0].EventID, got[0].EventID)
		require.Equal(t, events[1].EventID, got[1].EventID)
		require.True(t, !got[1].Timestamp.Before(got[0].Timestamp))
	})
}

func TestCreateSpanBatchPersistsToCassandra(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	traceID := uuid.New()
	spans := []*models.Span{
		{
			TraceID:     traceID,
			SpanID:      uuid.New(),
			ParentID:    uuid.Nil,
			ServiceName: "batch-test",
			StartTime:   time.Now().UTC().Truncate(time.Millisecond),
			DurationMs:  10,
			Tags:        map[string]string{"index": "0"},
		},
		{
			TraceID:     traceID,
			SpanID:      uuid.New(),
			ParentID:    uuid.Nil,
			ServiceName: "batch-test",
			StartTime:   time.Now().UTC().Add(5 * time.Millisecond).Truncate(time.Millisecond),
			DurationMs:  20,
			Tags:        map[string]string{"index": "1"},
		},
		{
			TraceID:     traceID,
			SpanID:      uuid.New(),
			ParentID:    uuid.Nil,
			ServiceName: "batch-test",
			StartTime:   time.Now().UTC().Add(10 * time.Millisecond).Truncate(time.Millisecond),
			DurationMs:  30,
			Tags:        map[string]string{"index": "2"},
		},
	}

	require.NoError(t, integrationRepo.CreateSpanBatch(ctx, spans))

	got, err := integrationRepo.ListSpansByTrace(ctx, traceID)
	require.NoError(t, err)
	require.Len(t, got, len(spans))
}
