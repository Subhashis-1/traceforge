//go:build integration
// +build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
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

	"github.com/Subhashis-1/traceforge/internal/ingestion"
	"github.com/Subhashis-1/traceforge/internal/otel"
	"github.com/Subhashis-1/traceforge/internal/storage"
)

const (
	testTraceID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
)

type playwrightResult struct {
	SessionID string `json:"sessionId"`
}

func TestSessionReplayEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	repoRoot := findRepoRoot(t)

	container := startCassandraContainer(ctx, t)
	defer func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Fatalf("terminate cassandra container: %v", err)
		}
	}()

	host, err := container.Host(ctx)
	require.NoError(t, err, "resolve cassandra host")

	mappedPort, err := container.MappedPort(ctx, "9042/tcp")
	require.NoError(t, err, "resolve cassandra port")

	systemSession := connectCassandraSession(ctx, t, host, mappedPort.Port(), "")
	defer systemSession.Close()

	require.NoError(t, systemSession.Query(`
		CREATE KEYSPACE IF NOT EXISTS traceforge
		WITH replication = {'class': 'SimpleStrategy', 'replication_factor': '1'}
		AND durable_writes = true`).Exec(), "create keyspace")

	appSession := connectCassandraSession(ctx, t, host, mappedPort.Port(), "traceforge")
	defer appSession.Close()

	applyMigrations(t, appSession, repoRoot)

	repo, err := storage.NewCassandraRepository([]string{net.JoinHostPort(host, mappedPort.Port())}, "traceforge")
	require.NoError(t, err, "create cassandra repository")
	defer func() {
		if closeErr := repo.Close(); closeErr != nil {
			t.Fatalf("close cassandra repository: %v", closeErr)
		}
	}()

	serverBaseURL, shutdownServer := startIngesterServer(t, repo, repoRoot)
	defer shutdownServer()

	sessionID := runPlaywright(t, ctx, repoRoot, serverBaseURL)
	verifySessionData(t, appSession, sessionID, uuid.MustParse(testTraceID))
}

func startCassandraContainer(ctx context.Context, t *testing.T) testcontainers.Container {
	t.Helper()

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "cassandra:4.1",
			ExposedPorts: []string{"9042/tcp"},
			WaitingFor:   wait.ForListeningPort("9042/tcp").WithStartupTimeout(5 * time.Minute),
		},
		Started: true,
	})
	require.NoError(t, err, "start cassandra container")
	return container
}

func connectCassandraSession(ctx context.Context, t *testing.T, host, port, keyspace string) *gocql.Session {
	t.Helper()

	address := net.JoinHostPort(host, port)
	var lastErr error

	for attempt := 0; attempt < 40; attempt++ {
		cluster := gocql.NewCluster(address)
		cluster.Timeout = 10 * time.Second
		cluster.ConnectTimeout = 10 * time.Second
		cluster.Consistency = gocql.Quorum
		if keyspace != "" {
			cluster.Keyspace = keyspace
		}

		session, err := cluster.CreateSession()
		if err == nil {
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			pingErr := session.Query(`SELECT release_version FROM system.local`).WithContext(pingCtx).Exec()
			cancel()
			if pingErr == nil {
				return session
			}
			lastErr = pingErr
			session.Close()
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("connect cassandra session: %v", ctx.Err())
		case <-time.After(3 * time.Second):
		}
	}

	t.Fatalf("connect cassandra session: %v", lastErr)
	return nil
}

func applyMigrations(t *testing.T, session *gocql.Session, repoRoot string) {
	t.Helper()

	paths := []string{
		filepath.Join(repoRoot, "internal", "storage", "schema.cql"),
		filepath.Join(repoRoot, "internal", "storage", "migrations", "2024_04_14_01_session_trace_map.cql"),
		filepath.Join(repoRoot, "internal", "storage", "migrations", "2026_04_13_01_events_by_session_trace_id.cql"),
	}

	for _, migrationPath := range paths {
		content, err := os.ReadFile(migrationPath)
		require.NoErrorf(t, err, "read migration file %s", migrationPath)

		for _, stmt := range splitStatements(string(content)) {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}

			if err := session.Query(stmt).Exec(); err != nil && !isIgnorableSchemaError(stmt, err) {
				t.Fatalf("apply migration %s failed: %v\nstatement: %s", migrationPath, err, stmt)
			}
		}
	}
}

func splitStatements(content string) []string {
	lines := strings.Split(content, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		filtered = append(filtered, line)
	}
	return strings.Split(strings.Join(filtered, "\n"), ";")
}

func isIgnorableSchemaError(stmt string, err error) bool {
	stmt = strings.ToUpper(strings.TrimSpace(stmt))
	errMsg := strings.ToLower(err.Error())

	if strings.HasPrefix(stmt, "ALTER TABLE ") && strings.Contains(stmt, " ADD ") {
		return strings.Contains(errMsg, "already exists") ||
			strings.Contains(errMsg, "duplicate column") ||
			strings.Contains(errMsg, "conflicts with an existing column") ||
			strings.Contains(errMsg, "invalid column name")
	}

	return false
}

func startIngesterServer(t *testing.T, repo storage.Repository, repoRoot string) (string, func()) {
	t.Helper()

	pipelineCtx, cancelPipeline := context.WithCancel(context.Background())
	pipeline := ingestion.NewPipeline(repo, 200, 4, 1000)
	pipeline.Start(pipelineCtx)

	sdkBundlePath := filepath.Join(repoRoot, "pkg", "sdk-js", "dist", "traceforge-sdk.js")
	sdkBundle, err := os.ReadFile(sdkBundlePath)
	require.NoError(t, err, "read sdk bundle")

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	e.GET("/live", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	e.GET("/", func(c echo.Context) error {
		return c.HTML(http.StatusOK, `<!doctype html>
<html>
  <head><meta charset="utf-8"><title>TraceForge Session Replay</title></head>
  <body>
    <button id="btn">Click</button>
    <script src="/traceforge-sdk.js"></script>
  </body>
</html>`)
	})

	e.GET("/traceforge-sdk.js", func(c echo.Context) error {
		return c.Blob(http.StatusOK, "application/javascript", sdkBundle)
	})

	e.POST("/v1/sessions", ingestion.NewSessionHandler(repo))
	e.POST("/v1/traces", echo.WrapHandler(otel.CorrelationMiddleware(repo)(ingestion.NewOTLPHTTPHandler(pipeline))))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "listen on random port")

	server := &http.Server{
		Handler:           e,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			errCh <- serveErr
		}
	}()

	baseURL := "http://" + listener.Addr().String()
	waitForLiveEndpoint(t, baseURL)

	return baseURL, func() {
		cancelPipeline()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil && err != http.ErrServerClosed {
			t.Fatalf("shutdown ingester server: %v", err)
		}

		select {
		case serveErr := <-errCh:
			t.Fatalf("ingester server failed: %v", serveErr)
		default:
		}
	}
}

func waitForLiveEndpoint(t *testing.T, baseURL string) {
	t.Helper()

	client := &http.Client{Timeout: 2 * time.Second}
	var lastErr error

	for attempt := 0; attempt < 50; attempt++ {
		resp, err := client.Get(baseURL + "/live")
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			return
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		lastErr = err
		time.Sleep(200 * time.Millisecond)
	}

	t.Fatalf("ingester /live never became ready: %v", lastErr)
}

func runPlaywright(t *testing.T, ctx context.Context, repoRoot, serverBaseURL string) uuid.UUID {
	t.Helper()

	scriptPath := filepath.Join(repoRoot, "scripts", "session_replay_test.js")
	cmd := exec.CommandContext(ctx, "node", scriptPath, serverBaseURL)
	cmd.Dir = repoRoot

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("playwright script failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	var result playwrightResult
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &result); err != nil {
		t.Fatalf("parse playwright result failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	sessionID, err := uuid.Parse(result.SessionID)
	if err != nil {
		t.Fatalf("invalid session id from playwright: %v\nstdout:\n%s", err, stdout.String())
	}

	return sessionID
}

func verifySessionData(t *testing.T, session *gocql.Session, sessionID, expectedTraceID uuid.UUID) {
	t.Helper()

	dateBucket := time.Now().UTC().Format("2006-01-02")
	cassandraSessionID := toGocqlUUID(sessionID)
	cassandraTraceID := toGocqlUUID(expectedTraceID)

	var (
		eventCount   int
		mappedTrace  gocql.UUID
		mappingFound bool
	)

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		eventCount = 0
		iter := session.Query(`
			SELECT event_id
			FROM events_by_session
			WHERE session_id = ? AND date_bucket = ?`,
			cassandraSessionID,
			dateBucket,
		).Iter()

		var eventID gocql.UUID
		for iter.Scan(&eventID) {
			eventCount++
		}
		if err := iter.Close(); err != nil {
			t.Fatalf("query events_by_session failed: %v", err)
		}

		err := session.Query(`
			SELECT trace_id
			FROM session_trace_map
			WHERE session_id = ?`,
			cassandraSessionID,
		).Scan(&mappedTrace)
		if err == nil {
			mappingFound = true
		}

		if eventCount > 0 && mappingFound && mappedTrace == cassandraTraceID {
			return
		}

		time.Sleep(500 * time.Millisecond)
	}

	t.Fatalf(
		"expected session replay rows not found: session_id=%s events=%d mapping_found=%t mapped_trace=%s expected_trace=%s",
		sessionID,
		eventCount,
		mappingFound,
		fromGocqlUUID(mappedTrace),
		expectedTraceID,
	)
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

func findRepoRoot(t *testing.T) string {
	t.Helper()

	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "resolve current file")
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}
