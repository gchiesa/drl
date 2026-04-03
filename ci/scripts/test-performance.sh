#!/usr/bin/env bash
# Run the performance test suite via docker compose.
# Usage: mise run test-performance
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
COMPOSE_FILE="$REPO_ROOT/test-suite/performance/docker-compose.yaml"
RESULTS_DIR="$REPO_ROOT/test-suite/performance/results"

mkdir -p "$RESULTS_DIR"

cleanup() {
  echo "==> Tearing down performance test infrastructure..."
  docker compose -f "$COMPOSE_FILE" down -v --remove-orphans 2>/dev/null || true
}
trap cleanup EXIT

echo "==> Building DRL image..."
docker compose -f "$COMPOSE_FILE" build

echo "==> Starting DRL cluster (3 replicas) + Envoy + echo-server..."
docker compose -f "$COMPOSE_FILE" up -d echo-server drl envoy

echo "==> Waiting for cluster to form and become healthy (20s)..."
sleep 20

echo "==> Cluster status:"
docker compose -f "$COMPOSE_FILE" ps

echo "==> Running performance k6 test (~3 min)..."
docker compose -f "$COMPOSE_FILE" run --rm k6

echo "==> Performance test completed."
if [ -f "$RESULTS_DIR/performance-report.html" ]; then
  echo "==> Report: $RESULTS_DIR/performance-report.html"
fi
