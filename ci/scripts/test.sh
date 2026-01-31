#!/usr/bin/env bash
set -euo pipefail

echo "Running tests..."
go test -v -race -coverprofile=coverage.out ./...
echo "Tests complete. Coverage report: coverage.out"
