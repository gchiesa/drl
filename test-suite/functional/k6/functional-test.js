import http from 'k6/http';
import { check } from 'k6';
import { Counter } from 'k6/metrics';
import { htmlReport } from "https://raw.githubusercontent.com/benc-uk/k6-reporter/main/dist/bundle.js";
import { textSummary } from "https://jslib.k6.io/k6-summary/0.0.2/index.js";

const TARGET_URL = __ENV.TARGET_URL || 'http://envoy:10000';
const SCENARIO_LABEL = __ENV.SCENARIO_LABEL || 'default';

// Configurable thresholds via environment variables.
// ALLOWED_THRESHOLD: maximum number of requests that may pass through (count<N).
// BLOCKED_THRESHOLD: minimum number of requests that must be blocked (count>N).
const allowedThreshold = parseInt(__ENV.ALLOWED_THRESHOLD || '35');
const blockedThreshold = parseInt(__ENV.BLOCKED_THRESHOLD || '50');

// Custom counters to track allowed vs blocked requests
const requestsAllowed = new Counter('requests_allowed');
const requestsBlocked = new Counter('requests_blocked');
const requestsOther   = new Counter('requests_other');

// DRL config: limit 30 req/min on /anything.
// We send 2 req/s for 45s = 90 total requests.
// Thresholds are parameterized per scenario to account for distribution overhead.
export const options = {
  scenarios: {
    steady_traffic: {
      executor: 'constant-arrival-rate',
      rate: 2,
      timeUnit: '1s',
      duration: '45s',
      preAllocatedVUs: 5,
      maxVUs: 10,
    },
  },
  thresholds: {
    'requests_blocked': [`count>${blockedThreshold}`],
    'requests_allowed': [`count<${allowedThreshold}`],
  },
};

export default function () {
  const res = http.get(`${TARGET_URL}/anything`);

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
}

export function handleSummary(data) {
  return {
    [`/results/functional-report-${SCENARIO_LABEL}.html`]: htmlReport(data),
    [`/results/functional-report-${SCENARIO_LABEL}.json`]: JSON.stringify(data, null, 2),
    stdout: textSummary(data, { indent: '  ', enableColors: true }),
  };
}
