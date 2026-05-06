#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

echo "=== Building DRL UI (Svelte + Vite) ==="
cd "${ROOT_DIR}/ui"
npm install --prefer-offline 2>/dev/null || npm install
npm run build
echo "UI build complete: ui/dist/index.html"

echo "=== Copying built UI into Go embed path ==="
cp "${ROOT_DIR}/ui/dist/index.html" "${ROOT_DIR}/internal/api/resources/index.html"
echo "Copied to internal/api/resources/index.html"

echo "=== Generating OpenAPI docs (swag) ==="
cd "${ROOT_DIR}"
swag init --parseDependency --parseInternal -g internal/api/api.go -o internal/api/docs
echo "OpenAPI docs generated: internal/api/docs/"

echo "=== Building DRL binary ==="
go build -o bin/drl ./main.go
echo "Build complete: bin/drl"
