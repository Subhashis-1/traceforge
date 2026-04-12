// Package main implements the Traceforge API server entry point.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/Subhashis-1/traceforge/internal/api"
	"github.com/Subhashis-1/traceforge/internal/otel"
	"github.com/Subhashis-1/traceforge/internal/storage"

	gootel "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
)

func main() {
	// Initialize OpenTelemetry tracing
	serviceName := "traceforge-api"
	ingestEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if ingestEndpoint == "" {
		ingestEndpoint = "localhost:4318" // default for local dev
	}
	shutdown, err := otel.InitTracer(serviceName, ingestEndpoint)
	if err != nil {
		log.Fatalf("Failed to initialize OpenTelemetry: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdown(ctx)
	}()
	// Parse CLI flags
	port := flag.Int("port", 8080, "HTTP server port")
	cassandraHosts := flag.String("cassandra-hosts", "localhost", "Comma-separated Cassandra hosts")
	keyspace := flag.String("keyspace", "traceforge", "Cassandra keyspace")
	flag.Parse()

	// Initialize Cassandra repository
	hosts := splitHosts(*cassandraHosts)
	repo, err := storage.NewCassandraRepository(hosts, *keyspace)
	if err != nil {
		log.Fatalf("Failed to initialize Cassandra repository: %v", err)
	}
	defer func() {
		if err := repo.Close(); err != nil {
			log.Printf("Failed to close Cassandra repository: %v", err)
		}
	}()

	// Create and configure Echo server
	e := initServer(repo)

	// Start server in a goroutine
	go func() {
		addr := fmt.Sprintf(":%d", *port)
		log.Printf("Starting HTTP server on %s", addr)
		if err := e.Start(addr); err != nil {
			log.Printf("HTTP server stopped: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := e.Shutdown(ctx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
}

// initServer creates and configures the Echo server instance.
// It wires up middleware and routes, but contains no business logic.
func initServer(repo storage.Repository) *echo.Echo {
	e := echo.New()

	// Middleware
	e.Use(middleware.Recover())
	e.Use(middleware.RequestLogger())

	// OpenTelemetry tracing middleware
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Extract context from incoming request
			ctx := c.Request().Context()
			prop := gootel.GetTextMapPropagator()
			ctx = prop.Extract(ctx, propagation.HeaderCarrier(c.Request().Header))

			endpoint := c.Path()
			tracer := otel.Tracer()
			start := time.Now()

			// Start span for this request
			spanCtx, span := tracer.Start(ctx, endpoint)
			defer span.End()

			// Set span context on request
			c.SetRequest(c.Request().WithContext(spanCtx))
			err := next(c)

			latency := time.Since(start)
			span.SetAttributes(
				attribute.String("endpoint", endpoint),
				attribute.String("method", c.Request().Method),
				attribute.String("trace_id", span.SpanContext().TraceID().String()),
				attribute.String("service", "traceforge-api"),
				attribute.Float64("latency_ms", float64(latency.Milliseconds())),
			)

			return err
		}
	})

	// Prometheus metrics registration
	var (
		apiRequests = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "api_requests_total",
				Help: "Total number of API requests",
			},
			[]string{"endpoint", "method", "status_code"},
		)
		apiErrors = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "api_errors_total",
				Help: "Total number of API error responses",
			},
			[]string{"endpoint", "method", "status_code"},
		)
		apiLatency = prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "api_latency_seconds",
				Help:    "API request latency in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"endpoint", "method", "status_code"},
		)
	)
	prometheus.MustRegister(apiRequests, apiErrors, apiLatency)

	// Prometheus metrics middleware
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			// Call next handler
			err := next(c)

			// Get route pattern (endpoint)
			endpoint := c.Path()
			method := c.Request().Method
			status := c.Response().Status
			statusCode := fmt.Sprintf("%d", status)

			apiRequests.WithLabelValues(endpoint, method, statusCode).Inc()
			if status >= 400 {
				apiErrors.WithLabelValues(endpoint, method, statusCode).Inc()
			}
			duration := time.Since(start).Seconds()
			apiLatency.WithLabelValues(endpoint, method, statusCode).Observe(duration)

			return err
		}
	})

	// Expose /metrics endpoint
	e.GET("/metrics", echo.WrapHandler(promhttp.Handler()))

	// Register generated API handlers
	handler := api.NewHandler(repo)
	api.RegisterHandlers(e, handler)

	return e
}

// splitHosts converts a comma-separated string of hosts into a slice.
func splitHosts(hosts string) []string {
	if hosts == "" {
		return []string{"localhost"}
	}
	parts := strings.Split(hosts, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
