import http from 'k6/http';
import { check, sleep } from 'k6';

// k6 Options: defines the execution stages of the load test
export const options = {
  stages: [
    { duration: '10s', target: 20 },  // Ramp up to 20 virtual users (VUs)
    { duration: '20s', target: 100 }, // Ramp up to 100 VUs and hold
    { duration: '10s', target: 0 },   // Cool down to 0 VUs
  ],
  thresholds: {
    // We expect the HTTP request duration to be fast (95% under 50ms)
    http_req_duration: ['p(95)<50'],
  },
};

// Target multiple ports to test the distributed rate-limiting capability.
// Make sure to run your Go servers on these ports.
const PORTS = ['8091', '8092'];

// A small, fixed pool of client IPs. This allows the load test to quickly exceed
// the configured rate limit capacity per IP (e.g. 5 requests/min on /login)
// and verifies that the server correctly returns 429 status codes.
const CLIENT_IPS = [
  '192.168.1.10',
  '192.168.1.11',
  '192.168.1.12',
  '192.168.1.13',
  '192.168.1.14',
];

export default function () {
  // Randomly choose an IP from the fixed pool
  const ip = CLIENT_IPS[Math.floor(Math.random() * CLIENT_IPS.length)];

  // Randomly choose one of the running server instances
  const port = PORTS[Math.floor(Math.random() * PORTS.length)];

  // Distribute traffic across different endpoints with different rate limits:
  // - /login (limit: 5 requests per minute)
  // - /api (limit: 10 requests per minute)
  // - / (default limit: 100 requests per minute)
  const paths = ['/login', '/api', '/'];
  const path = paths[Math.floor(Math.random() * paths.length)];

  const url = `http://localhost:${port}${path}`;
  const params = {
    headers: {
      'X-Forwarded-For': ip,
    },
  };

  const response = http.get(url, params);

  // We check that the server responds with either 200 (OK) or 429 (Too Many Requests).
  // Any other status code (like 500) indicates an issue with the rate limiter implementation.
  check(response, {
    'status is 200 (Allowed) or 429 (Rate Limited)': (r) => r.status === 200 || r.status === 429,
    'status is not 500 (Internal Server Error)': (r) => r.status !== 500,
  });

  // Short sleep to simulate real-world request spacing per VU
  sleep(0.05);
}
