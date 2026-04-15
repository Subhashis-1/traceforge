// Package main implements the Traceforge ingester service entry point.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	collecttracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"

	"github.com/Subhashis-1/traceforge/internal/ingestion"
	"github.com/Subhashis-1/traceforge/internal/otel"
	"github.com/Subhashis-1/traceforge/internal/storage"
)

const cassandraKeyspace = "traceforge"

type config struct {
	httpPort       int
	grpcPort       int
	cassandraHosts []string
	batchSize      int
	maxConcurrency int
	queueSize      int
}

func main() {
	cfg := parseFlags()

	log.Printf(
		"starting ingester http_port=%d grpc_port=%d cassandra_hosts=%s batch_size=%d max_concurrency=%d queue_size=%d",
		cfg.httpPort,
		cfg.grpcPort,
		strings.Join(cfg.cassandraHosts, ","),
		cfg.batchSize,
		cfg.maxConcurrency,
		cfg.queueSize,
	)

	repo, err := storage.NewCassandraRepository(cfg.cassandraHosts, cassandraKeyspace)
	if err != nil {
		log.Panicf("initialize cassandra repository: %v", err)
	}
	defer func() {
		if err := repo.Close(); err != nil {
			log.Printf("close cassandra repository: %v", err)
		}
	}()

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shutdownSignals := make(chan os.Signal, 1)
	signal.Notify(shutdownSignals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(shutdownSignals)

	pipeline := ingestion.NewPipeline(repo, cfg.batchSize, cfg.maxConcurrency, cfg.queueSize)
	pipeline.Start(rootCtx)

	httpServer := startHTTPServer(cfg, repo, func(_ context.Context) error {
		readyRepo, err := storage.NewCassandraRepository(cfg.cassandraHosts, cassandraKeyspace)
		if err != nil {
			return err
		}
		defer func() {
			if closeErr := readyRepo.Close(); closeErr != nil {
				log.Printf("close readiness cassandra repository: %v", closeErr)
			}
		}()
		return nil
	}, ingestion.NewOTLPHTTPHandler(pipeline))

	grpcServer, grpcListener := startGRPCServer(cfg, ingestion.NewOTLPTraceGRPCServer(pipeline))

	sig := <-shutdownSignals
	cancel()
	log.Printf("received shutdown signal=%s", sig.String())

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown http server: %v", err)
	}

	done := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
	case <-shutdownCtx.Done():
		log.Printf("grpc graceful shutdown timed out, forcing stop")
		grpcServer.Stop()
	}

	if err := grpcListener.Close(); err != nil {
		log.Printf("close grpc listener: %v", err)
	}
}

func parseFlags() config {
	httpPort := flag.Int("http-port", 4318, "HTTP health server port")
	grpcPort := flag.Int("grpc-port", 4317, "gRPC listener port")
	cassandraHosts := flag.String("cassandra-hosts", "localhost", "comma-separated Cassandra contact points")
	batchSize := flag.Int("batch-size", 200, "ingestion batch size")
	maxConcurrency := flag.Int("max-concurrency", 4, "maximum concurrent write workers")
	queueSize := flag.Int("queue-size", 100000, "maximum buffered queue size")
	flag.Parse()

	var hosts []string
	for _, host := range strings.Split(*cassandraHosts, ",") {
		trimmed := strings.TrimSpace(host)
		if trimmed != "" {
			hosts = append(hosts, trimmed)
		}
	}
	if len(hosts) == 0 {
		hosts = []string{"localhost"}
	}

	return config{
		httpPort:       *httpPort,
		grpcPort:       *grpcPort,
		cassandraHosts: hosts,
		batchSize:      *batchSize,
		maxConcurrency: *maxConcurrency,
		queueSize:      *queueSize,
	}
}

func startHTTPServer(cfg config, repo storage.Repository, readinessCheck func(context.Context) error, tracesHandler http.Handler) *http.Server {
	addr := fmt.Sprintf(":%d", cfg.httpPort)
	e := echo.New()

	e.GET("/live", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	e.POST("/v1/traces", echo.WrapHandler(otel.CorrelationMiddleware(repo)(tracesHandler)))
	ingestion.RegisterSessionRoutes(e, repo)
	e.GET("/metrics", echo.WrapHandler(promhttp.Handler()))

	e.GET("/ready", func(c echo.Context) error {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
		defer cancel()

		if err := readinessCheck(ctx); err != nil {
			log.Printf("readiness check failed: %v", err)
			return c.String(http.StatusInternalServerError, "cassandra unavailable")
		}

		return c.String(http.StatusOK, "ok")
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           e,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("http server listening addr=%s", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Panicf("http server failed: %v", err)
		}
	}()

	return server
}

func startGRPCServer(cfg config, traceServer collecttracev1.TraceServiceServer) (*grpc.Server, net.Listener) {
	addr := fmt.Sprintf(":%d", cfg.grpcPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Panicf("listen grpc: %v", err)
	}

	server := grpc.NewServer()
	collecttracev1.RegisterTraceServiceServer(server, traceServer)

	go func() {
		log.Printf("grpc server listening addr=%s", addr)
		if err := server.Serve(listener); err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
			log.Panicf("grpc server failed: %v", err)
		}
	}()

	return server, listener
}
