#!/usr/bin/env bash
set -euo pipefail

echo "Running linter..."
golangci-lint run ./...
echo "Linting complete."
