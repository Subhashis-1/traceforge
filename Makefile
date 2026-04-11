# Makefile for Trace Forge

export GOCACHE := $(CURDIR)/.cache/go-build
export npm_config_cache := $(CURDIR)/.cache/npm

.PHONY: run run-ingest run-api ui test lint docker-up docker-build-backend docker-push migrate

# Run ingestion service: Start Cassandra and run the ingestion binary
run: run-ingest

run-ingest:
	docker compose -f deployments/docker-compose.yml up -d cassandra
	go run ./cmd/traceforge

# Run API service: Start Cassandra and run the API binary (placeholder)
run-api:
	docker compose -f deployments/docker-compose.yml up -d cassandra
	go run ./cmd/traceforge

# Run UI development server
ui:
	cd web && npm install && npm run dev

# Run tests for Go and Node.js
test:
	go test ./...
	cd web && npm install && npm run test

# Run linting for Go and Node.js
lint:
	golangci-lint run ./...
	cd web && npm install && npm run lint

# Start local Docker dependencies
docker-up:
	docker compose -f deployments/docker-compose.yml up -d

# Build the backend Docker image
docker-build-backend:
	docker build -f deployments/Dockerfile.backend -t ghcr.io/myorg/traceforge-backend:latest .

# Push the backend Docker image
docker-push:
	docker push ghcr.io/myorg/traceforge-backend:latest

# Run CQL migrations against Cassandra
migrate:
	docker compose -f deployments/docker-compose.yml up -d cassandra
	@echo "Waiting for Cassandra to be ready..."
	@powershell -Command "Start-Sleep -Seconds 5"
	go run ./cmd/migrate --cassandra-host=localhost --cql-dir=./internal/storage
