#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# Install protoc-gen-go if not already available
if ! command -v protoc-gen-go &>/dev/null; then
  echo "Installing protoc-gen-go..."
  go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
fi

echo "Generating Go code from protobuf definitions..."
protoc \
  --proto_path="${ROOT_DIR}" \
  --go_out="${ROOT_DIR}" \
  --go_opt=paths=source_relative \
  internal/proto/accounting.proto

echo "Protobuf generation complete."
