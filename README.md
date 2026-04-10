<<<<<<< HEAD
# traceforge
Open-source observability platform for distributed tracing, session replay, and event correlation
=======
# Trace Forge

[![CI](https://github.com/Subhashis-1/traceforge/actions/workflows/ci.yml/badge.svg)](https://github.com/Subhashis-1/traceforge/actions/workflows/ci.yml)
[![Docker](https://img.shields.io/badge/docker-ghcr.io%2Fmyorg%2Ftraceforge--backend:latest-blue)](https://ghcr.io/myorg/traceforge-backend)
[![Contribute](https://img.shields.io/badge/contribute-open--source-green)](https://github.com/Subhashis-1/traceforge/blob/main/CONTRIBUTING.md)

An open-source observability platform for microservices, enabling distributed tracing and session replay across 50+ services to reduce debugging time by over 60%.

## Quick Start

1. **Install Docker Compose**: Ensure Docker and Docker Compose are installed on your system.
2. **Clone the repository**:
   ```bash
   git clone https://github.com/Subhashis-1/traceforge.git
   cd traceforge
   ```
3. **Run the ingestion service**:
   ```bash
   make run-ingest
   ```
4. **Run the UI**:
   ```bash
   make ui
   ```
   Open [http://localhost:5173](http://localhost:5173) in your browser.

## Development Workflow

1. **Lint and test**: Run `make lint` and `make test` to ensure code quality.
2. **Make changes**: Edit code in the appropriate directories (Go in `internal/`, UI in `web/`).
3. **Commit and push**: Use conventional commits, then push to a feature branch.
4. **Open a PR**: Create a pull request for review and CI checks.

For detailed contribution guidelines, see [CONTRIBUTING.md](CONTRIBUTING.md).
>>>>>>> 65e5bc3 (phase 0)
