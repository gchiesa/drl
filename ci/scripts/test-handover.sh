#!/usr/bin/env bash
# Run the handover functional test.
#
# Validates that when a DRL instance is gracefully shut down, its accounting
# state is correctly transferred to the remaining instance, which then
# enforces rate limits for ALL entities (including those previously owned by
# the stopped node).
#
# Test flow:
#   Phase 1 — Start 2 DRL instances, generate 15 requests per entity across
#              20 distinct entities (via X-Test-Entity header). All must be
#              allowed (15 < 30/min limit).
#   Handover — Scale DRL 2 → 1. The stopping instance evacuates its
#              accounting state to the survivor via the handover protocol.
#   Phase 2 — Generate 20 more requests per entity. Because phase 1
#              accumulated 15 hits (now transferred), blocking kicks in at
#              the 16th request per entity (15+16=31 > 30). We expect ≥ 60
#              blocked requests (≈ 80 with full transfer; ≈ 40 without).
#
# Environment overrides:
#   INITIAL_REPLICAS    — starting DRL replica count  (default: 2)
#   CLUSTER_WAIT        — seconds to wait for cluster  (default: 15)
#   HANDOVER_WAIT       — seconds to wait post scale-down (default: 20)
#   PHASE1_BLOCKED_MAX  — max allowed blocks in phase 1 (default: 10)
#   PHASE2_BLOCKED_MIN  — min required blocks in phase 2 (default: 60)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
COMPOSE_FILE="$REPO_ROOT/test-suite/handover/docker-compose.yaml"
RESULTS_DIR="$REPO_ROOT/test-suite/handover/results"

INITIAL_REPLICAS="${INITIAL_REPLICAS:-2}"
CLUSTER_WAIT="${CLUSTER_WAIT:-15}"
HANDOVER_WAIT="${HANDOVER_WAIT:-20}"
PHASE1_BLOCKED_MAX="${PHASE1_BLOCKED_MAX:-10}"
PHASE2_BLOCKED_MIN="${PHASE2_BLOCKED_MIN:-60}"

mkdir -p "$RESULTS_DIR"

cleanup() {
  echo "==> Tearing down handover test infrastructure..."
  docker compose -f "$COMPOSE_FILE" down -v --remove-orphans 2>/dev/null || true
}
trap cleanup EXIT

echo "========================================"
echo "==> Handover Test"
echo "==>   Initial DRL replicas : ${INITIAL_REPLICAS}"
echo "==>   Cluster settle       : ${CLUSTER_WAIT}s"
echo "==>   Handover settle      : ${HANDOVER_WAIT}s"
echo "==>   Phase 1 blocked max  : < ${PHASE1_BLOCKED_MAX}"
echo "==>   Phase 2 blocked min  : > ${PHASE2_BLOCKED_MIN}"
echo "========================================"

echo "==> Building DRL image..."
docker compose -f "$COMPOSE_FILE" build

echo "==> Starting DRL cluster (${INITIAL_REPLICAS} replicas) + Envoy + echo-server + k6..."
docker compose -f "$COMPOSE_FILE" up -d \
  --scale drl="${INITIAL_REPLICAS}" \
  echo-server drl envoy jumpbox k6

echo "==> Waiting for cluster to form (${CLUSTER_WAIT}s)..."
sleep "$CLUSTER_WAIT"

echo "==> Cluster status:"
docker compose -f "$COMPOSE_FILE" ps

echo "========================================"
echo "==> Phase 1: Pre-handover traffic"
echo "==>   20 entities x 15 requests — all must be allowed (below 30/min)"
echo "========================================"
docker compose -f "$COMPOSE_FILE" exec -T \
  -e SCENARIO_LABEL="handover-phase1" \
  -e PHASE="1" \
  -e BLOCKED_MAX="${PHASE1_BLOCKED_MAX}" \
  k6 k6 run /scripts/handover-test.js

echo "==> Phase 1 complete."
echo ""
echo "==> Entities accounting in DRL-2:"
docker compose -f "$COMPOSE_FILE" exec jumpbox sh -c 'curl --digest -u "admin:$DRL_PRIVATE_API_KEY" handover-drl-2:8082/accounting/stats'
echo ""
echo "==> Entities accounting in DRL-1:"
docker compose -f "$COMPOSE_FILE" exec jumpbox sh -c 'curl --digest -u "admin:$DRL_PRIVATE_API_KEY" handover-drl-1:8082/accounting/stats'
echo ""

echo "==> Scaling DRL down from ${INITIAL_REPLICAS} → 1 to trigger handover..."
# --no-recreate keeps the surviving DRL-1 container running unchanged;
# Docker stops the extra instance(s) gracefully (SIGTERM → handover → exit).
docker compose -f "$COMPOSE_FILE" up -d \
  --scale drl=1 \
  --no-recreate \
  drl

echo "==> Waiting for handover to complete and Envoy to detect cluster change (${HANDOVER_WAIT}s)..."
sleep "$HANDOVER_WAIT"

echo "==> Cluster status after scale-down:"
docker compose -f "$COMPOSE_FILE" ps

echo "==> Entities accounting in DRL:"
docker compose -f "$COMPOSE_FILE" exec jumpbox sh -c 'curl --digest -u "admin:$DRL_PRIVATE_API_KEY" handover-drl-1:8082/accounting/stats'
echo ""

echo "========================================"
echo "==> Phase 2: Post-handover traffic"
echo "==>   20 entities x 20 requests — blocking must start after request #16"
echo "==>   (15 carried over via handover + 16 new = 31 > 30/min limit)"
echo "==>   Threshold: >= ${PHASE2_BLOCKED_MIN} blocked (handover OK ≈ 80; failed ≈ 40)"
echo "========================================"
docker compose -f "$COMPOSE_FILE" exec -T \
  -e SCENARIO_LABEL="handover-phase2" \
  -e PHASE="2" \
  -e BLOCKED_THRESHOLD="${PHASE2_BLOCKED_MIN}" \
  k6 k6 run /scripts/handover-test.js

echo "==> Handover Test: PASSED"
if ls "$RESULTS_DIR"/handover-report-*.html 1>/dev/null 2>&1; then
  echo "==> Reports: $RESULTS_DIR/"
fi
