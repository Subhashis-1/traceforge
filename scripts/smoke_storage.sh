#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: bash scripts/smoke_storage.sh

Starts the local Cassandra dependency, waits for it to become ready,
applies storage migrations, and runs the storage unit + integration tests.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

if [[ $# -ne 0 ]]; then
  usage
  exit 1
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required but was not found in PATH" >&2
  exit 1
fi

if ! command -v make >/dev/null 2>&1; then
  echo "make is required but was not found in PATH" >&2
  exit 1
fi

echo "Starting Cassandra..."
docker compose -f deployments/docker-compose.yml up -d cassandra

echo "Waiting for Cassandra health check..."
for attempt in $(seq 1 30); do
  if docker compose -f deployments/docker-compose.yml exec -T cassandra \
    cqlsh -e "DESCRIBE KEYSPACES;" >/dev/null 2>&1; then
    break
  fi

  if [[ "$attempt" -eq 30 ]]; then
    echo "Cassandra did not become ready in time" >&2
    exit 1
  fi

  sleep 5
done

echo "Running migrations..."
make migrate

echo "Running storage test suite..."
make test-storage

echo "✅ Trace Forge storage layer is healthy"
