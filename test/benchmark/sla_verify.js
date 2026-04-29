import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';

// Custom metrics
const apiSuccessRate = new Rate('api_success_rate');
const healthLatency = new Trend('health_latency_ms');
const callsLatency = new Trend('calls_latency_ms');

// SLA Thresholds from docs/performance/sla_baseline.md
export const options = {
  scenarios: {
    health_check: {
      executor: 'constant-arrival-rate',
      rate: 10,
      timeUnit: '1s',
      duration: '2m',
      preAllocatedVUs: 5,
      exec: 'healthCheck',
    },
    api_calls: {
      executor: 'constant-arrival-rate',
      rate: 5,
      timeUnit: '1s',
      duration: '2m',
      preAllocatedVUs: 10,
      exec: 'apiCalls',
      startTime: '10s',
    },
  },
  thresholds: {
    'api_success_rate': ['rate>0.999'],
    'health_latency_ms': ['p(95)<20'],
    'calls_latency_ms': ['p(95)<150'],
    'http_req_failed': ['rate<0.001'],
  },
};

const BASE_URL = __ENV.VBGW_URL || 'http://localhost:8080';
const API_KEY = __ENV.ADMIN_API_KEY || 'changeme-admin-key';
const headers = {
  'Authorization': `Bearer ${API_KEY}`,
  'Content-Type': 'application/json',
};

export function healthCheck() {
  const res = http.get(`${BASE_URL}/live`);
  healthLatency.add(res.timings.duration);
  apiSuccessRate.add(res.status === 200);
  check(res, {
    'health status 200': (r) => r.status === 200,
    'health latency < 20ms': (r) => r.timings.duration < 20,
  });
}

export function apiCalls() {
  const capRes = http.get(`${BASE_URL}/api/v1/admin/services/capacity`, { headers });
  apiSuccessRate.add(capRes.status === 200);
  callsLatency.add(capRes.timings.duration);
  check(capRes, {
    'capacity status 200': (r) => r.status === 200,
    'capacity latency < 150ms': (r) => r.timings.duration < 150,
  });

  const sessRes = http.get(`${BASE_URL}/api/v1/admin/sessions/active`, { headers });
  apiSuccessRate.add(sessRes.status === 200);

  const routeRes = http.get(`${BASE_URL}/api/v1/admin/routing/config`, { headers });
  apiSuccessRate.add(routeRes.status === 200);

  sleep(0.1);
}

export function handleSummary(data) {
  const passed = Object.values(data.root_group.checks).every(c => c.passes > 0 && c.fails === 0);
  console.log(`\nVBGW SLA Verification: ${passed ? 'PASSED' : 'FAILED'}`);
  return {};
}
