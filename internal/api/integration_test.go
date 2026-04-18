//go:build integration
// +build integration

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/time/rate"

	"github.com/Subhashis-1/traceforge/internal/models"
	"github.com/Subhashis-1/traceforge/internal/storage"
)

var (
	apiIntegrationContainer testcontainers.Container
	apiIntegrationRepo      *storage.CassandraRepository
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

	apiIntegrationContainer = container

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

	repo, err := createAPIIntegrationRepository(ctx, host, port.Port())
	if err != nil {
		fmt.Fprintf(os.Stderr, "initialize integration repository: %v\n", err)
		_ = container.Terminate(ctx)
		os.Exit(1)
	}
	apiIntegrationRepo = repo

	exitCode := func() int {
		defer func() {
			if apiIntegrationRepo != nil {
				_ = apiIntegrationRepo.Close()
			}
			if apiIntegrationContainer != nil {
				_ = apiIntegrationContainer.Terminate(context.Background())
			}
		}()

		return m.Run()
	}()

	os.Exit(exitCode)
}

func createAPIIntegrationRepository(ctx context.Context, host, port string) (*storage.CassandraRepository, error) {
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
			return nil, fmt.Errorf("create system session: %w", ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
	if err != nil {
		return nil, fmt.Errorf("create system session: %w", err)
	}
	defer systemSession.Close()

	if err := systemSession.Query(`
		CREATE KEYSPACE IF NOT EXISTS traceforge
		WITH replication = {
			'class': 'SimpleStrategy',
			'replication_factor': '1'
		}
		AND durable_writes = true`,
	).Exec(); err != nil {
		return nil, fmt.Errorf("create keyspace: %w", err)
	}

	cluster.Keyspace = "traceforge"
	appSession, err := cluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("create app session: %w", err)
	}
	defer appSession.Close()

	schemaPath, err := apiSchemaPath()
	if err != nil {
		return nil, fmt.Errorf("resolve schema path: %w", err)
	}

	if err := applyAPISchema(appSession, schemaPath); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	repo, err := storage.NewCassandraRepository([]string{fmt.Sprintf("%s:%s", host, port)}, "traceforge")
	if err != nil {
		return nil, fmt.Errorf("create repository: %w", err)
	}

	return repo, nil
}

func apiSchemaPath() (string, error) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("runtime caller unavailable")
	}

	return filepath.Join(filepath.Dir(currentFile), "..", "storage", "schema.cql"), nil
}

func applyAPISchema(session *gocql.Session, schemaPath string) error {
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("read schema file: %w", err)
	}

	for _, stmt := range splitAPIStatements(string(schemaBytes)) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}

		if err := session.Query(stmt).Exec(); err != nil {
			if isIgnorableAPIError(stmt, err) {
				continue
			}
			return fmt.Errorf("exec schema statement %q: %w", stmt, err)
		}
	}

	return nil
}

func splitAPIStatements(content string) []string {
	lines := strings.Split(content, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		filtered = append(filtered, line)
	}

	return strings.Split(strings.Join(filtered, "\n"), ";")
}

func isIgnorableAPIError(stmt string, err error) bool {
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

func startAPIIntegrationServer(t *testing.T, repo storage.Repository) string {
	t.Helper()

	resetAPIIntegrationRateLimits()

	prevRPS, hadRPS := os.LookupEnv("RATE_LIMIT_RPS")
	prevBurst, hadBurst := os.LookupEnv("RATE_LIMIT_BURST")
	require.NoError(t, os.Setenv("RATE_LIMIT_RPS", "10"))
	require.NoError(t, os.Setenv("RATE_LIMIT_BURST", "1"))

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(CORS)
	e.Use(APIKeyAuth(map[string]struct{}{"demo-key": {}}))
	e.Use(RateLimit)
	e.Use(middleware.Recover())

	v1 := e.Group("/api/v1")
	RegisterTraceRoutes(v1.Group("/traces"), repo)
	RegisterSessionRoutes(v1.Group("/sessions"), repo)
	RegisterSearchRoutes(v1, repo)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := &http.Server{
		Handler:           e,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		_ = server.Serve(listener)
	}()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)

		if hadRPS {
			_ = os.Setenv("RATE_LIMIT_RPS", prevRPS)
		} else {
			_ = os.Unsetenv("RATE_LIMIT_RPS")
		}

		if hadBurst {
			_ = os.Setenv("RATE_LIMIT_BURST", prevBurst)
		} else {
			_ = os.Unsetenv("RATE_LIMIT_BURST")
		}

		resetAPIIntegrationRateLimits()
	})

	return "http://" + listener.Addr().String()
}

func resetAPIIntegrationRateLimits() {
	rateLimitersMu.Lock()
	rateLimiters = make(map[string]*rate.Limiter)
	rateLimitersMu.Unlock()
	rateLimitOnce = sync.Once{}
}

func newAPIIntegrationRequest(t *testing.T, method, rawURL string) *http.Request {
	t.Helper()

	req, err := http.NewRequest(method, rawURL, nil)
	require.NoError(t, err)
	req.Header.Set("X-API-Key", "demo-key")
	return req
}

func TestAPIIntegration(t *testing.T) {
	require.NotNil(t, apiIntegrationRepo)

	t.Run("DSL to CQL correctness", func(t *testing.T) {
		baseURL := startAPIIntegrationServer(t, apiIntegrationRepo)
		client := &http.Client{Timeout: 10 * time.Second}

		serviceName := "search-" + uuid.NewString()[:8]
		traceID := uuid.New()
		status := 503
		startTime := time.Now().UTC().Truncate(time.Second)

		require.NoError(t, apiIntegrationRepo.CreateTrace(context.Background(), &models.Trace{
			TraceID:     traceID,
			ServiceName: serviceName,
			StartTime:   startTime,
			RootSpanID:  uuid.New(),
			DurationMs:  250,
			Status:      status,
			Tags: map[string]string{
				"user_id": "42",
				"env":     "integration",
			},
		}))

		rawQuery := fmt.Sprintf("service:%s status:>=%d", serviceName, status)
		endpoint := baseURL + "/api/v1/search?q=" + url.QueryEscape(rawQuery)

		resp, err := client.Do(newAPIIntegrationRequest(t, http.MethodGet, endpoint))
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var body searchResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		require.NotEmpty(t, body.Hits)

		var matched *models.Trace
		for _, hit := range body.Hits {
			if hit.TraceID == traceID {
				matched = hit
				break
			}
		}

		require.NotNil(t, matched, "expected inserted trace to be returned by search")
		require.Equal(t, traceID, matched.TraceID)
		require.Equal(t, serviceName, matched.ServiceName)
		require.Equal(t, status, matched.Status)
		require.Equal(t, int64(250), matched.DurationMs)
		require.Equal(t, "42", matched.Tags["user_id"])
	})

	t.Run("Pagination and cursor", func(t *testing.T) {
		baseURL := startAPIIntegrationServer(t, apiIntegrationRepo)
		client := &http.Client{Timeout: 10 * time.Second}

		serviceName := "page-" + uuid.NewString()[:8]
		start := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)

		for i := 0; i < 30; i++ {
			require.NoError(t, apiIntegrationRepo.CreateTrace(context.Background(), &models.Trace{
				TraceID:     uuid.New(),
				ServiceName: serviceName,
				StartTime:   start.Add(time.Duration(i) * time.Second),
				RootSpanID:  uuid.New(),
				DurationMs:  int64(100 + i),
				Status:      200,
				Tags:        map[string]string{"page": "true", "index": fmt.Sprintf("%d", i)},
			}))
		}

		from := start.Add(-time.Second).Format(time.RFC3339)
		to := start.Add(31 * time.Second).Format(time.RFC3339)

		firstURL := fmt.Sprintf(
			"%s/api/v1/traces?service=%s&from=%s&to=%s&limit=10",
			baseURL,
			url.QueryEscape(serviceName),
			url.QueryEscape(from),
			url.QueryEscape(to),
		)

		firstResp, err := client.Do(newAPIIntegrationRequest(t, http.MethodGet, firstURL))
		require.NoError(t, err)
		defer firstResp.Body.Close()

		require.Equal(t, http.StatusOK, firstResp.StatusCode)

		var firstPage traceListResponse
		require.NoError(t, json.NewDecoder(firstResp.Body).Decode(&firstPage))
		require.Len(t, firstPage.Traces, 10)
		require.NotEmpty(t, firstPage.NextCursor)

		time.Sleep(250 * time.Millisecond)

		secondURL := fmt.Sprintf(
			"%s/api/v1/traces?service=%s&from=%s&to=%s&limit=10&cursor=%s",
			baseURL,
			url.QueryEscape(serviceName),
			url.QueryEscape(from),
			url.QueryEscape(to),
			url.QueryEscape(firstPage.NextCursor),
		)

		secondResp, err := client.Do(newAPIIntegrationRequest(t, http.MethodGet, secondURL))
		require.NoError(t, err)
		defer secondResp.Body.Close()

		require.Equal(t, http.StatusOK, secondResp.StatusCode)

		var secondPage traceListResponse
		require.NoError(t, json.NewDecoder(secondResp.Body).Decode(&secondPage))
		require.Len(t, secondPage.Traces, 10)

		seen := make(map[uuid.UUID]struct{}, 20)
		for _, trace := range firstPage.Traces {
			seen[trace.TraceID] = struct{}{}
		}
		for _, trace := range secondPage.Traces {
			_, exists := seen[trace.TraceID]
			require.False(t, exists, "trace %s appeared on both pages", trace.TraceID)
			seen[trace.TraceID] = struct{}{}
		}

		require.Len(t, seen, 20)
	})

	t.Run("Rate limit header", func(t *testing.T) {
		baseURL := startAPIIntegrationServer(t, apiIntegrationRepo)
		client := &http.Client{Timeout: 10 * time.Second}

		serviceName := "ratelimit-" + uuid.NewString()[:8]
		endpoint := baseURL + "/api/v1/search?q=" + url.QueryEscape("service:"+serviceName)

		statuses := make([]int, 0, 12)
		retryAfter := make([]string, 0, 12)

		for i := 0; i < 12; i++ {
			resp, err := client.Do(newAPIIntegrationRequest(t, http.MethodGet, endpoint))
			require.NoError(t, err)
			statuses = append(statuses, resp.StatusCode)
			retryAfter = append(retryAfter, resp.Header.Get("Retry-After"))
			resp.Body.Close()

			if i < 9 {
				time.Sleep(110 * time.Millisecond)
			}
		}

		require.Equal(t, http.StatusTooManyRequests, statuses[10], "expected 11th response to be rate limited")
		require.Equal(t, http.StatusTooManyRequests, statuses[11], "expected 12th response to be rate limited")
		require.NotEmpty(t, retryAfter[10], "expected Retry-After header on 11th response")
		require.NotEmpty(t, retryAfter[11], "expected Retry-After header on 12th response")
	})
}
