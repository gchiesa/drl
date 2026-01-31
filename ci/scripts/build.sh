#!/usr/bin/env bash
set -euo pipefail

echo "Building DRL..."
#go build -race -o bin/drl ./main.go
go build -o bin/drl ./main.go
echo "Build complete: bin/drl"
