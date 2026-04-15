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

echo "=== Building DRL binary ==="
cd "${ROOT_DIR}"
go build -o bin/drl ./main.go
echo "Build complete: bin/drl"
