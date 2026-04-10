#!/bin/bash

# Setup directories for Trace Forge project

# Entry points for binaries
mkdir -p cmd

# Core business logic (private code)
mkdir -p internal/ingestion
mkdir -p internal/query
mkdir -p internal/storage
mkdir -p internal/otel
mkdir -p internal/replay
mkdir -p internal/models

# API contract definitions
mkdir -p api

# Public libraries (SDKs)
mkdir -p pkg/sdk-go
mkdir -p pkg/sdk-js

# Frontend dashboard
mkdir -p web/components
mkdir -p web/hooks
mkdir -p web/styles

# Deployments
mkdir -p deployments

# Scripts
mkdir -p scripts

# Docs
mkdir -p docs

echo "Directory structure for Trace Forge created successfully."