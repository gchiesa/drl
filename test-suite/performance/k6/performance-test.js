import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter, Trend, Rate } from 'k6/metrics';
import { htmlReport } from "https://raw.githubusercontent.com/benc-uk/k6-reporter/main/dist/bundle.js";
import { textSummary } from "https://jslib.k6.io/k6-summary/0.0.2/index.js";

const TARGET_URL = __ENV.TARGET_URL || 'http://envoy:10000';
const NUM_SIMULATED_IPS = parseInt(__ENV.NUM_SIMULATED_IPS || '50');

// Custom metrics
const drlLatency = new Trend('drl_decision_latency', true);
const requestsAllowed = new Counter('requests_allowed');
const requestsBlocked = new Counter('requests_blocked');
const requestsErrored = new Counter('requests_errored');
const errorRate = new Rate('drl_error_rate');

// Performance test: ramp up to high concurrency over 3 minutes.
// Each VU simulates a unique client IP via X-Forwarded-For header.
//
// Both 200 (allowed) and 429 (blocked) are valid DRL decisions — they both
// represent DRL processing the request correctly. Only non-200/429 responses
// are actual errors (e.g. timeouts, 5xx from infrastructure).
//
// We measure:
//   - p50, p95, p99 response latency across ALL DRL decisions (200 + 429)
//   - Throughput (requests/s)
//   - Error rate (only non-200/429 responses)
export const options = {
  scenarios: {
    ramp_up: {
      executor: 'ramping-vus',
      startVUs: 5,
      stages: [
        { duration: '30s', target: 20 },   // warm up
        { duration: '60s', target: 50 },   // steady state
        { duration: '60s', target: 100 },  // peak load
        { duration: '30s', target: 0 },    // cool down
      ],
    },
  },
  thresholds: {
    // DRL decision latency targets (end-to-end through Envoy, for both 200 and 429)
    'drl_decision_latency': ['p(50)<3', 'p(95)<7', 'p(99)<10'],
    // Only infrastructure errors count — 429 is a valid DRL decision, not an error
    'drl_error_rate': ['rate<0.001'],
  },
};

// Generate a pool of simulated IPs for consistent distribution
const ipPool = Array.from({ length: NUM_SIMULATED_IPS }, (_, i) => {
  const octet3 = Math.floor(i / 256);
  const octet4 = (i % 256) + 1;
  return `10.${octet3}.${octet4}.1`;
});

export default function () {
  // Each VU picks an IP from the pool based on its VU id
  const clientIP = ipPool[__VU % ipPool.length];

  const params = {
    headers: {
      'X-Forwarded-For': clientIP,
    },
  };

  const res = http.get(`${TARGET_URL}/anything`, params);

  // Track DRL decision latency for all valid responses (200 + 429)
  const isDrlDecision = res.status === 200 || res.status === 429;
  if (isDrlDecision) {
    drlLatency.add(res.timings.duration);
  }

  // Categorize responses
  if (res.status === 200) {
    requestsAllowed.add(1);
  } else if (res.status === 429) {
    requestsBlocked.add(1);
  } else {
    requestsErrored.add(1);
  }

  // Error rate: only non-200/429 are errors (timeouts, 5xx, connection failures)
  errorRate.add(!isDrlDecision);

  check(res, {
    'valid DRL response (200 or 429)': (r) => r.status === 200 || r.status === 429,
    'response time < 500ms': (r) => r.timings.duration < 500,
  });

  // Small sleep to avoid overwhelming at extreme rates
  sleep(0.05);
}

export function handleSummary(data) {
  return {
    '/results/performance-report.html': htmlReport(data),
    '/results/performance-report.json': JSON.stringify(data, null, 2),
    stdout: textSummary(data, { indent: '  ', enableColors: true }),
  };
}
