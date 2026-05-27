import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter } from 'k6/metrics';

const TARGET_URL = __ENV.TARGET_URL || 'http://localhost:8080';

// Custom counters so the summary shows allowed vs rate-limited requests
// separately — useful for confirming that DRL is actually enforcing limits.
const rateLimited = new Counter('rate_limited_requests');
const allowed     = new Counter('allowed_requests');

export const options = {
    stages: [
        { duration: '5s',  target: 2  },  // warm-up
        { duration: '10s', target: 5  },  // ramp-up: approaching the /anything limit
        { duration: '20s', target: 10 },  // steady: should trigger 429 on /anything
        { duration: '15s', target: 15 },  // peak: heavy rate-limit pressure
        { duration: '10s', target: 0  },  // ramp-down
    ],
    // The embedded proxy returns 429 once a rule fires, so we allow a high
    // rate-limited fraction and only fail on true server errors (5xx).
    thresholds: {
        http_req_duration:           ['p(95)<800'],
        'http_req_failed{status:429}': ['rate<0.9'],  // 429s are expected
        'http_req_failed{status:500}': ['rate<0.01'], // 5xx should be rare
    },
};

export default function () {
    const res = http.get(`${TARGET_URL}/anything`);

    if (res.status === 429) {
        rateLimited.add(1);
    } else {
        allowed.add(1);
    }

    check(res, {
        // The proxy should never return a 5xx; 200 and 429 are both valid.
        'no server error': (r) => r.status < 500,
        // When allowed, the upstream echo-server always returns a body.
        'has body when 200': (r) => r.status !== 200 || r.body.length > 0,
    });

    sleep(0.1);
}
