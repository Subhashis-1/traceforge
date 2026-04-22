# Trace Forge

[![CI](https://github.com/Subhashis-1/traceforge/actions/workflows/ci.yml/badge.svg)](https://github.com/Subhashis-1/traceforge/actions/workflows/ci.yml)
[![Docker](https://img.shields.io/badge/docker-ghcr.io%2Fmyorg%2Ftraceforge--backend:latest-blue)](https://ghcr.io/myorg/traceforge-backend)
[![Contribute](https://img.shields.io/badge/contribute-open--source-green)](CONTRIBUTING.md)

Trace Forge is an open-source observability platform for distributed tracing and session replay.
It is designed to ingest high-volume telemetry, persist it in Cassandra, and serve a low-latency
trace and session exploration experience through a React dashboard.

## What It Includes

- OTLP trace ingestion over HTTP and gRPC
- Cassandra-backed storage with query-first tables
- API endpoints for trace lists, trace detail, session events, search, and latency metrics
- React UI with trace browsing, filtering, virtualization, and replay navigation
- Docker Compose, CI, linting, tests, and local developer tooling

## Repository Layout

- `cmd/` - Go entrypoints for the ingester, API, and migration tooling
- `internal/` - Storage, ingestion, API, telemetry, and model packages
- `pkg/` - Reusable packages, including the JavaScript SDK
- `web/` - React frontend and frontend build tooling
- `deployments/` - Dockerfiles and Compose definitions
- `scripts/` - Smoke tests, load scripts, and helper utilities

## Quick Start

### Prerequisites

- Docker Desktop or Docker Engine with Compose
- Go 1.22 or newer
- Node.js 20 or newer
- GNU Make

### Local Development

Bring up Cassandra and supporting services:

```bash
make docker-up
```

Apply migrations:

```bash
make migrate
```

Run the ingester:

```bash
make run-ingest
```

Run the API:

```bash
make run-api
```

Run the frontend:

```bash
make ui
```

The UI is available at `http://localhost:5173`.

## Production Overview

Trace Forge is structured for a split control plane and data plane:

- `ingester` receives OTLP traffic and writes spans and events to Cassandra.
- `api` serves read traffic for traces, sessions, and search.
- `web` is a static React application served behind a web server or CDN.

Recommended production defaults:

- Run Cassandra as a managed or hardened cluster, not a single-node dev instance.
- Run the ingester and API behind an ingress or load balancer.
- Keep API keys and service credentials in secret storage.
- Set `CORS_ORIGINS` explicitly for non-local deployments.
- Use `LOCAL_QUORUM` consistency for low-latency reads where appropriate.

## Useful Commands

```bash
make test
make test-storage
make test-integration
make lint
make docker-up
```

## Validation

Use these checks when preparing a release:

```bash
go test ./...
go test -tags=integration ./...
cd web && npm run lint
cd web && npm run build
```

For local Cassandra validation, run:

```bash
make migrate
make run-ingest
```

## Configuration

Environment variables commonly used by the services:

- `API_HOST`, `API_PORT`
- `CORS_ORIGINS`
- `RATE_LIMIT_RPS`, `RATE_LIMIT_BURST`
- `REACT_APP_API_URL`, `REACT_APP_API_KEY`
- Cassandra host and keyspace flags passed to the Go binaries

## Observability

- `ingester` exposes `/live`, `/ready`, and `/metrics`
- `api` exposes `/healthz`
- The storage layer is covered by unit and integration tests

## Contributing

Please read [CONTRIBUTING.md](CONTRIBUTING.md) before sending patches.
The project expects:

- Go code formatted with `gofmt`
- No lint errors from the Go or web toolchains
- Tests passing locally before opening a pull request

## License

This project is released under the MIT License. See [LICENSE](LICENSE).
