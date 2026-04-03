import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter, Trend } from 'k6/metrics';
import { htmlReport } from "https://raw.githubusercontent.com/benc-uk/k6-reporter/main/dist/bundle.js";
import { textSummary } from "https://jslib.k6.io/k6-summary/0.0.2/index.js";

const TARGET_URL = __ENV.TARGET_URL || 'http://envoy:10000';
const NUM_SIMULATED_IPS = parseInt(__ENV.NUM_SIMULATED_IPS || '50');

// Custom metrics
const drlLatency = new Trend('drl_decision_latency', true);
const requestsTotal = new Counter('requests_total');

// Performance test: ramp up to high concurrency over 3 minutes.
// Each VU simulates a unique client IP via X-Forwarded-For header.
// DRL limit is 10000/min per entity, so blocking should not occur.
//
// We measure:
//   - p50, p95, p99 response latency (including DRL decision time)
//   - Throughput (requests/s)
//   - Error rate under load
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
    // DRL decision latency targets (end-to-end through Envoy)
    'http_req_duration': ['p(50)<50', 'p(95)<200', 'p(99)<500'],
    // Custom DRL latency trend
    'drl_decision_latency': ['p(50)<50', 'p(95)<200', 'p(99)<500'],
    // Error rate should stay under 1% (no rate limiting expected)
    'http_req_failed': ['rate<0.01'],
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

  const start = Date.now();
  const res = http.get(`${TARGET_URL}/anything`, params);
  const elapsed = Date.now() - start;

  drlLatency.add(elapsed);
  requestsTotal.add(1);

  check(res, {
    'status is 200': (r) => r.status === 200,
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
