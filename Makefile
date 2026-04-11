# Makefile for Trace Forge

export GOCACHE := $(CURDIR)/.cache/go-build
export npm_config_cache := $(CURDIR)/.cache/npm

.PHONY: run run-ingest run-api ui test test-storage test-integration lint docker-up docker-build-backend docker-push migrate gen-mocks

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

# Run storage unit + integration tests
test-storage: ## Run unit + integration tests for the storage layer
	@echo "Running unit tests..."
	go test ./internal/storage -run '^Test' -count=1
	@echo "Running integration tests (requires Docker)..."
	go test ./internal/storage -tags=integration -run '^Test' -count=1

# Run tests for Go and Node.js
test: test-storage
	go test ./...
	cd web && npm install && npm run test

# Run integration tests (requires Docker)
test-integration:
	go test -tags=integration -v ./internal/storage

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

# Generate mocks for unit testing
gen-mocks:
	go run github.com/golang/mock/cmd/mockgen@latest -source=internal/storage/repository.go -destination=internal/storage/mock_repository.go -package=storage
