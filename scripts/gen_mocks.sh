#!/bin/bash
# Generate mocks for Trace Forge
# This script uses mockgen (golang/mock) to generate interface mocks
# Mocks are used for unit testing without requiring a live Cassandra instance

set -e

echo "Generating mocks..."

# Generate Repository mock
echo "  → Generating Repository mock..."
go run github.com/golang/mock/cmd/mockgen@latest \
  -source=internal/storage/repository.go \
  -destination=internal/storage/mock_repository.go \
  -package=storage

echo "✅ Mocks generated successfully"
echo "   - internal/storage/mock_repository.go"
