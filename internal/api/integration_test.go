package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Subhashis-1/traceforge/internal/models"
	"github.com/Subhashis-1/traceforge/internal/storage"
)

func setupCassandraContainer(t *testing.T) (testcontainers.Container, string) {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)

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
	require.NoError(t, err)

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "9042/tcp")
	require.NoError(t, err)
	addr := fmt.Sprintf("%s:%s", host, port.Port())

	cluster := gocql.NewCluster(addr)
	cluster.Keyspace = "system"
	cluster.Consistency = gocql.Quorum
	cluster.Timeout = 10 * time.Second
	cluster.ConnectTimeout = 10 * time.Second

	var systemSession *gocql.Session
	for attempt := 0; attempt < 45; attempt++ {
		systemSession, err = cluster.CreateSession()
		if err == nil {
			if pingErr := systemSession.Query(`SELECT release_version FROM system.local`).Exec(); pingErr == nil {
				break
			}
			systemSession.Close()
			err = fmt.Errorf("cassandra not ready for CQL yet")
		}
		time.Sleep(2 * time.Second)
	}
	require.NoError(t, err)
	defer systemSession.Close()

	require.NoError(t, systemSession.Query(`
		CREATE KEYSPACE IF NOT EXISTS traceforge
		WITH replication = {'class': 'SimpleStrategy', 'replication_factor': '1'}
		AND durable_writes = true`).Exec())

	cluster.Keyspace = "traceforge"
	appSession, err := cluster.CreateSession()
	require.NoError(t, err)
	defer appSession.Close()

	schemaPath := schemaFilePath(t)
	schema, err := os.ReadFile(schemaPath)
	require.NoError(t, err)
	for _, stmt := range splitCQLStatements(string(schema)) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		err = appSession.Query(stmt).Exec()
		if err != nil {
			require.True(t, isIgnorableSchemaError(stmt, err), "schema query failed: %s (%v)", stmt, err)
		}
	}

	return container, addr
}

func schemaFilePath(t *testing.T) string {
	t.Helper()

	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Join(filepath.Dir(currentFile), "..", "storage", "schema.cql")
}

func splitCQLStatements(schema string) []string {
	lines := strings.Split(schema, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		filtered = append(filtered, line)
	}
	return strings.Split(strings.Join(filtered, "\n"), ";")
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

func TestAPIIntegration(t *testing.T) {
	cass, cassAddr := setupCassandraContainer(t)
	defer func() {
		if err := cass.Terminate(context.Background()); err != nil {
			t.Logf("failed to terminate cassandra container: %v", err)
		}
	}()

	repo, err := storage.NewCassandraRepository([]string{cassAddr}, "traceforge")
	require.NoError(t, err)
	defer func() {
		if err := repo.Close(); err != nil {
			t.Logf("failed to close repository: %v", err)
		}
	}()

	service := "svc-test"
	startTime := time.Now().UTC().Truncate(time.Second)

	traceIDs := make([]uuid.UUID, 0, 6)
	for i := 0; i < 6; i++ {
		traceID := uuid.New()
		rootSpanID := uuid.New()
		traceIDs = append(traceIDs, traceID)

		trace := &models.Trace{
			TraceID:     traceID,
			ServiceName: service,
			StartTime:   startTime.Add(time.Duration(i) * time.Second),
			RootSpanID:  rootSpanID,
			DurationMs:  123,
			Status:      0,
			Tags:        map[string]string{"env": "test", "index": fmt.Sprintf("%d", i)},
		}
		require.NoError(t, repo.CreateTrace(context.Background(), trace))
		require.NoError(t, repo.CreateSpan(context.Background(), &models.Span{
			TraceID:     traceID,
			SpanID:      rootSpanID,
			ParentID:    uuid.Nil,
			ServiceName: service,
			StartTime:   trace.StartTime,
			DurationMs:  trace.DurationMs,
			Tags:        trace.Tags,
		}))
	}

	sessionID := uuid.New()
	event := &models.Event{
		SessionID: sessionID,
		EventID:   uuid.New(),
		Timestamp: startTime,
		Payload:   []byte(`{"foo":"bar"}`),
		Tags:      map[string]string{"env": "test"},
	}
	require.NoError(t, repo.CreateEvent(context.Background(), event))

	e := echo.New()
	RegisterHandlers(e, NewHandler(repo))

	measure := func(name string, f func()) {
		begin := time.Now()
		f()
		require.Less(t, time.Since(begin), 200*time.Millisecond, "%s exceeded API latency SLA", name)
	}

	var firstPage TraceListResponse
	measure("ListTraces page 1", func() {
		req := httptest.NewRequest(
			http.MethodGet,
			"/traces?service="+service+"&from="+startTime.Format(time.RFC3339)+"&to="+startTime.Add(10*time.Second).Format(time.RFC3339)+"&limit=3",
			nil,
		)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &firstPage))
		require.Len(t, firstPage.Traces, 3)
		require.NotNil(t, firstPage.NextCursor)
	})

	var secondPage TraceListResponse
	measure("ListTraces page 2", func() {
		req := httptest.NewRequest(
			http.MethodGet,
			"/traces?service="+service+"&from="+startTime.Format(time.RFC3339)+"&to="+startTime.Add(10*time.Second).Format(time.RFC3339)+"&limit=3&cursor="+*firstPage.NextCursor,
			nil,
		)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &secondPage))
		require.Len(t, secondPage.Traces, 3)
	})

	seen := make(map[string]struct{}, 6)
	for _, trace := range append(firstPage.Traces, secondPage.Traces...) {
		if _, exists := seen[trace.TraceId.String()]; exists {
			t.Fatalf("duplicate trace returned across pages: %s", trace.TraceId)
		}
		seen[trace.TraceId.String()] = struct{}{}
	}
	require.Len(t, seen, 6)

	measure("TraceDetail", func() {
		req := httptest.NewRequest(http.MethodGet, "/traces/"+traceIDs[0].String(), nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	})

	measure("ListSpans", func() {
		req := httptest.NewRequest(http.MethodGet, "/traces/"+traceIDs[0].String()+"/spans", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	})

	measure("ListEvents", func() {
		req := httptest.NewRequest(
			http.MethodGet,
			"/sessions/"+sessionID.String()+"/events?from="+startTime.Add(-time.Second).Format(time.RFC3339)+"&to="+startTime.Add(time.Minute).Format(time.RFC3339),
			nil,
		)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	})
}
