#!/usr/bin/env bash
# Run the functional test suite via docker compose.
# Usage: mise run test-functional
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
COMPOSE_FILE="$REPO_ROOT/test-suite/functional/docker-compose.yaml"
RESULTS_DIR="$REPO_ROOT/test-suite/functional/results"

mkdir -p "$RESULTS_DIR"

cleanup() {
  echo "==> Tearing down functional test infrastructure..."
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

echo "==> Running functional k6 test..."
docker compose -f "$COMPOSE_FILE" run --rm k6

echo "==> Functional test completed."
if [ -f "$RESULTS_DIR/functional-report.html" ]; then
  echo "==> Report: $RESULTS_DIR/functional-report.html"
fi
