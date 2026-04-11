# Testing Guide

This document describes how to run tests for Trace Forge.

## Unit Tests

Unit tests are self-contained and do not require any external dependencies.

```bash
# Run all unit tests
go test ./...

# Run tests with coverage
go test ./... -coverprofile=coverage.txt
go tool cover -html=coverage.txt

# Run specific test package
go test ./internal/storage -v

# Run specific test
go test ./internal/storage -run TestCreateTrace -v
```

Unit tests can be run on any platform without Docker installed.

## Integration Tests

Integration tests verify the CassandraRepository against a real Cassandra instance using [testcontainers-go](https://github.com/testcontainers/testcontainers-go).

### Prerequisites

- Docker installed and running (required for testcontainers)
- Go 1.22+

### Running Integration Tests

Integration tests are marked with `// +build integration` build tag and only execute when explicitly requested.

```bash
# Run integration tests for storage package
go test -tags=integration -v ./internal/storage

# Run only specific integration test
go test -tags=integration -run TestWriteIdempotent -v ./internal/storage

# Run all tests including integration
go test -tags=integration ./...

# Using Make target
make test-integration
```

### What Each Integration Test Does

#### TestWriteIdempotent

- Creates a trace with a fixed UUID
- Calls `CreateTrace` twice with the same trace
- Queries the database directly to verify only 1 row exists
- **Purpose**: Verifies unlogged batch idempotency

#### TestReadLatency

- Inserts 10 random traces for a service
- Calls `ListTraces` and measures execution time
- Asserts latency < 50ms (realistic threshold for Docker)
- **Purpose**: Performance profiling and regression detection

#### TestSpansAndEvents

- Creates multiple spans and verifies roundtrip (insert → retrieve)
- Creates events and verifies ordering (timestamps in ascending order)
- **Purpose**: Tests denormalized data structure queries

#### TestTraceBlobRoundtrip

- Stores a serialized trace blob
- Retrieves it and verifies byte-for-byte equality
- **Purpose**: Tests blob storage optimization for direct trace access

#### TestMultiServiceQuery

- Inserts traces for multiple services
- Queries each service independently
- Verifies service filtering is correct
- **Purpose**: Ensures partition key filtering works

### Container Lifecycle

The `TestMain` function handles:

1. **Setup**: Starts a Cassandra 4.1 Docker container via testcontainers
2. **Wait**: Waits for container to be ready (checks for "created default superuser role" log)
3. **Schema**: Applies all required tables (traces_by_service, spans_by_trace, events_by_session, trace_by_id)
4. **Initialization**: Creates a `CassandraRepository` with the session
5. **Test Execution**: Runs all test functions
6. **Cleanup**: Terminates the container and closes the session

### Troubleshooting

**Container fails to start**

```
Error: docker daemon not running
```

→ Ensure Docker is installed and running: `docker ps`

**Port already in use**

```
Error: failed to get mapped port
```

→ Testcontainers should auto-assign available ports. Check for leftover containers: `docker ps -a`

**Cassandra initialization timeout**

```
Error: wait timeout (60s)
```

→ Cassandra takes time to start. Timeout can be adjusted in `startCassandraContainer()`

**Tests skip on CI/CD**

- Integration tests automatically skip if run with `-short` flag
- CI/CD should only run integration tests if Docker is available
- Unit tests do NOT require Docker

## Benchmarking

Run benchmarks for specific functions:

```bash
# Benchmark date bucketing logic
go test ./internal/storage -bench BenchmarkDateBucketing -benchmem

# Benchmark trace data construction
go test ./internal/storage -bench BenchmarkCreateTraceDataConstruction -benchmem
```

## CI/CD Integration

The `.github/workflows/ci.yml` runs:

- **Unit tests** on every push/PR (no Docker required)
- **Linting** with golangci-lint
- **Coverage reporting** with coverage badges

Integration tests can be added as a separate optional job that requires Docker.

## Coverage Goals

- **Unit Tests**: Target 80%+ coverage of business logic
- **Integration Tests**: Verify critical paths (write idempotency, read latency, multi-service queries)
- **E2E Tests**: Verify complete workflows (trace ingestion → query → visualization)

## Build Tags

Tests respect Go build tags:

- `// +build integration` - Only runs with `-tags=integration`
- No tag - Runs by default

This allows CI/CD to:

```bash
# Fast unit tests only
go test ./...

# Comprehensive tests with integration
go test -tags=integration ./...
```
