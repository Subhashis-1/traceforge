# Makefile for Trace Forge

.PHONY: run-ingest run-api ui test lint docker-build-backend docker-push

# Run ingestion service: Start Cassandra and run the ingestion binary
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

# Build the backend Docker image
docker-build-backend:
	docker build -f deployments/Dockerfile.backend -t ghcr.io/myorg/traceforge-backend:latest .

# Push the backend Docker image
docker-push:
	docker push ghcr.io/myorg/traceforge-backend:latest
