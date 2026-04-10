import http from 'k6/http';
import { check, sleep } from 'k6';

const TARGET_URL = __ENV.TARGET_URL || 'http://localhost:10000';

export const options = {
    stages: [
        { duration: '5s', target: 1 },   // 10% ramp-up
        { duration: '10s', target: 3 },  // 30% ramp-up
        { duration: '15s', target: 5 },  // 50% ramp-up
        { duration: '30s', target: 10 }, // 100% (10 VUs)
    ],
    thresholds: {
        http_req_duration: ['p(95)<500'],
        http_req_failed: ['rate<0.01'],
    },
};

export default function () {
    const res = http.get(`${TARGET_URL}/anything`);

    check(res, {
        'status is 200': (r) => r.status === 200,
        'response has body': (r) => r.body.length > 0,
    });

    sleep(0.1);
}
