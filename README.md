# Trace Forge

[![CI](https://github.com/Subhashis-1/traceforge/actions/workflows/ci.yml/badge.svg)](https://github.com/Subhashis-1/traceforge/actions/workflows/ci.yml)
[![Docker](https://img.shields.io/badge/docker-ghcr.io%2Fmyorg%2Ftraceforge--backend:latest-blue)](https://ghcr.io/myorg/traceforge-backend)
[![Contribute](https://img.shields.io/badge/contribute-open--source-green)](https://github.com/Subhashis-1/traceforge/blob/main/CONTRIBUTING.md)

An open-source observability platform for microservices, enabling distributed tracing and session replay across 50+ services to reduce debugging time by over 60%.

## Setup

1. Install the local toolchain:
   - Docker Desktop with Docker Compose
   - Go 1.22+
   - Node.js 20+
   - GNU Make
2. Clone the repository:
   ```bash
   git clone https://github.com/Subhashis-1/traceforge.git
   cd traceforge
   ```
3. Install pre-commit hooks:
   ```bash
   pre-commit install
   ```

## Docker Services

Start Cassandra and the local builder container:

```bash
make docker-up
```

## Backend

Run the placeholder Go service:

```bash
make run
```

## Frontend

Start the Vite development server:

```bash
make ui
```

Open [http://localhost:5173](http://localhost:5173) in your browser.

## Makefile Usage

Common targets:

```bash
make docker-up
make lint
make test
make run
make ui
```

## Development Workflow

1. Bring up local dependencies:
   ```bash
   make docker-up
   ```
2. Run lint and tests:
   ```bash
   make lint
   make test
   ```
3. Start the backend:
   ```bash
   make run
   ```
4. Start the frontend:
   ```bash
   make ui
   ```
5. Make changes and open a pull request after `make lint` and `make test` pass.

For detailed contribution guidelines, see [CONTRIBUTING.md](CONTRIBUTING.md).

## Known Issues

- The current validation path depends on Docker being available locally for Cassandra-backed integration coverage.
- The bundled load scripts are useful for smoke testing, but they are not a substitute for sustained multi-node production benchmarking.
