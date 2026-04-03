#!/usr/bin/env bash
# Run a single functional test scenario.
#
# Usage:
#   test-functional-scenario.sh <label> <replicas> <allowed_threshold> <blocked_threshold> <wait_seconds>
#
# Example:
#   test-functional-scenario.sh single-instance 1 33 55 10
set -euo pipefail

if [ $# -ne 5 ]; then
  echo "Usage: $0 <label> <replicas> <allowed_threshold> <blocked_threshold> <wait_seconds>"
  exit 1
fi

LABEL="$1"
REPLICAS="$2"
ALLOWED_THRESHOLD="$3"
BLOCKED_THRESHOLD="$4"
WAIT_TIME="$5"

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
COMPOSE_FILE="$REPO_ROOT/test-suite/functional/docker-compose.yaml"
RESULTS_DIR="$REPO_ROOT/test-suite/functional/results"

mkdir -p "$RESULTS_DIR"

cleanup() {
  echo "==> Tearing down functional test infrastructure..."
  docker compose -f "$COMPOSE_FILE" down -v --remove-orphans 2>/dev/null || true
}
trap cleanup EXIT

echo "========================================"
echo "==> Scenario: ${LABEL} (${REPLICAS} DRL replica(s))"
echo "==>   allowed < ${ALLOWED_THRESHOLD}, blocked > ${BLOCKED_THRESHOLD}"
echo "========================================"

echo "==> Building DRL image..."
docker compose -f "$COMPOSE_FILE" build

echo "==> Starting DRL cluster (${REPLICAS} replica(s)) + Envoy + echo-server..."
docker compose -f "$COMPOSE_FILE" up -d --scale drl="${REPLICAS}" echo-server drl envoy

echo "==> Waiting for cluster to form and become healthy (${WAIT_TIME}s)..."
sleep "$WAIT_TIME"

echo "==> Cluster status:"
docker compose -f "$COMPOSE_FILE" ps

echo "==> Running k6 test for scenario: ${LABEL}..."
docker compose -f "$COMPOSE_FILE" run --rm \
  -e SCENARIO_LABEL="${LABEL}" \
  -e ALLOWED_THRESHOLD="${ALLOWED_THRESHOLD}" \
  -e BLOCKED_THRESHOLD="${BLOCKED_THRESHOLD}" \
  k6

echo "==> Scenario ${LABEL}: PASSED"
if [ -f "$RESULTS_DIR/functional-report-${LABEL}.html" ]; then
  echo "==> Report: $RESULTS_DIR/functional-report-${LABEL}.html"
fi
