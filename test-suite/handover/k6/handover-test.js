import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter } from 'k6/metrics';
import { htmlReport } from "https://raw.githubusercontent.com/benc-uk/k6-reporter/main/dist/bundle.js";
import { textSummary } from "https://jslib.k6.io/k6-summary/0.0.2/index.js";

const TARGET_URL    = __ENV.TARGET_URL    || 'http://envoy:10000';
const SCENARIO_LABEL = __ENV.SCENARIO_LABEL || 'handover-phase1';
const PHASE         = __ENV.PHASE         || '1';
const NUM_ENTITIES  = 20;

// ─── Rate-limit maths ────────────────────────────────────────────────────────
//
// DRL rule: 30 req/min per entity (entity = source-IP + rule-path + x-test-entity).
//
// Phase 1 (pre-handover)
//   20 entities × 15 requests = 300 total.
//   15 < 30 → all requests must be allowed.
//
// Phase 2 (post-handover)
//   DRL-2 was shut down and its accounting was transferred to DRL-1.
//   Each entity therefore enters phase 2 with a count of 15.
//
//   20 entities × 20 requests = 400 total.
//   • requests 1–15  : count → 16–30  → OK  (still within limit)
//   • request  16    : count → 31     → OK returned optimistically, then
//                                        async Process() adds entity to blocklist
//   • requests 17–20 : blocklist hit  → 429 BLOCKED
//
//   Expected blocks: 20 entities × 4 blocks = 80.
//   Threshold set to 60 to tolerate minor timing variance.
//
//   Without handover only ~10 entities (owned by DRL-1) would be transferred,
//   giving ≈ 40 blocks — well below the 60 threshold.
// ─────────────────────────────────────────────────────────────────────────────

const BLOCKED_MAX = parseInt(__ENV.BLOCKED_MAX       || '10'); // phase 1
const BLOCKED_MIN = parseInt(__ENV.BLOCKED_THRESHOLD || '60'); // phase 2

const requestsAllowed = new Counter('requests_allowed');
const requestsBlocked = new Counter('requests_blocked');
const requestsOther   = new Counter('requests_other');

const iterationsPerVU = PHASE === '1' ? 15 : 20;

export const options = {
  scenarios: {
    handover_traffic: {
      executor: 'per-vu-iterations',
      vus: NUM_ENTITIES,
      iterations: iterationsPerVU,
      maxDuration: '120s',
    },
  },
  thresholds: PHASE === '1' ? {
    // Phase 1: nearly all requests must be allowed; very few (if any) blocked.
    'requests_allowed': ['count>250'],
    'requests_blocked': [`count<${BLOCKED_MAX}`],
  } : {
    // Phase 2: enough blocks must be observed to confirm handover transferred
    // accounting state from the stopped DRL instance.
    'requests_blocked': [`count>${BLOCKED_MIN}`],
  },
};

export default function () {
  // Each VU gets a unique entity ID (VU IDs are 1-based; we keep them 1–20).
  // The same VU→entity mapping is preserved across both phase 1 and phase 2
  // runs because k6 always starts VU numbering at 1.
  const entityId = `entity-${((__VU - 1) % NUM_ENTITIES) + 1}`;

  const params = {
    headers: {
      // Envoy normalises header names to lowercase in HTTP/2 CheckRequests.
      // The DRL rule references "x-test-entity" (lowercase) so that each
      // unique value creates a distinct accounting entity in the hash ring.
      'X-Test-Entity': entityId,
    },
  };

  const res = http.get(`${TARGET_URL}/anything`, params);

  check(res, {
    'status is 200 or 429': (r) => r.status === 200 || r.status === 429,
  });

  if (res.status === 200) {
    requestsAllowed.add(1);
  } else if (res.status === 429) {
    requestsBlocked.add(1);
  } else {
    requestsOther.add(1);
  }

  // Brief pause between iterations so DRL's async accounting flusher (200ms
  // interval) has time to propagate increments to the owner node before the
  // next request. Without this, rapid-fire requests may all return 200 before
  // the counter reaches the blocking threshold.
  sleep(0.1);
}

export function handleSummary(data) {
  return {
    [`/results/handover-report-${SCENARIO_LABEL}.html`]: htmlReport(data),
    [`/results/handover-report-${SCENARIO_LABEL}.json`]: JSON.stringify(data, null, 2),
    stdout: textSummary(data, { indent: '  ', enableColors: true }),
  };
}
