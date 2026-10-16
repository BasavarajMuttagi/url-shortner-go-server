import http from 'k6/http';
import { check } from 'k6';
import { Trend, Counter } from 'k6/metrics';

// =============================================================================
// Custom Metrics for Granular Route Breakdown
// =============================================================================
export const redirectDuration = new Trend('redirect_duration', true);
export const createDuration = new Trend('create_duration', true);
export const successfulOps = new Counter('successful_operations');

// =============================================================================
// Benchmark Configuration (Grafana k6 Multi-Stage Stress Testing Lifecycle)
// =============================================================================
export const options = {
  discardResponseBodies: true,
  scenarios: {
    // Official 4-Stage Open-Loop Lifecycle
    // 1. Warm-up -> 2. Sustained Plateau -> 3. Peak Stress -> 4. Recovery
    standard_stress_lifecycle: {
      executor: 'ramping-arrival-rate',
      startRate: 500,
      timeUnit: '1s',
      preAllocatedVUs: parseInt(__ENV.PRE_VUS || '300'),
      maxVUs: parseInt(__ENV.MAX_VUS || '5000'),
      stages: [
        { target: 2500, duration: '15s' },  // Stage 1: Warm-up ramp
        { target: 5000, duration: '20s' },  // Stage 2: Normal production plateau
        { target: 10000, duration: '20s' }, // Stage 3: Extreme stress peak
        { target: 1000, duration: '15s' },  // Stage 4: Cool-down & recovery verification
      ],
    },
  },
  thresholds: {
    // Production Service Level Objectives (SLOs)
    http_req_failed: ['rate<0.01'],              // Less than 1% error rate
    http_req_duration: ['p(95)<50', 'p(99)<100'], // 95% of requests < 50ms, 99% < 100ms
    redirect_duration: ['p(95)<30'],             // Hot-path redirects p95 < 30ms
    checks: ['rate>0.99'],                       // 99%+ checks passed
  },
};

const BASE_URL = __ENV.BASE_URL ? __ENV.BASE_URL.replace(/\/+$/, '') : 'http://localhost:8080';
const SHORT_KEY = __ENV.SHORT_KEY || 'godoc2026';

// Randomized headers for realistic geo-distribution and user-agent parsing
const userAgents = [
  'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36',
  'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15',
  'Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:129.0) Gecko/20100101 Firefox/129.0',
  'Mozilla/5.0 (Linux; Android 14; SM-S918B) AppleWebKit/537.36 Mobile Safari/537.36',
];

const referrers = [
  'https://twitter.com/dev/status/123',
  'https://news.ycombinator.com/item?id=456',
  'https://reddit.com/r/golang',
  'https://github.com/golang/go',
  '',
];

const countries = ['US', 'GB', 'IN', 'DE', 'JP', 'FR', 'CA', 'SG'];

function randomHeaders() {
  return {
    'User-Agent': userAgents[Math.floor(Math.random() * userAgents.length)],
    'Referer': referrers[Math.floor(Math.random() * referrers.length)],
    'CF-IPCountry': countries[Math.floor(Math.random() * countries.length)],
    'CF-Connecting-IP': '198.51.100.' + (Math.floor(Math.random() * 250) + 1),
  };
}

function randomKey(len) {
  const chars = 'abcdefghijklmnopqrstuvwxyz0123456789';
  let out = '';
  for (let i = 0; i < len; i++) {
    out += chars.charAt(Math.floor(Math.random() * chars.length));
  }
  return out;
}

export default function () {
  const roll = Math.random() * 100;
  const headers = randomHeaders();

  if (roll < 95) {
    // =========================================================================
    // 95%: Hot-Path Redis Cached Redirects (GET /:shortKey)
    // =========================================================================
    const res = http.get(BASE_URL + '/' + SHORT_KEY, {
      headers: headers,
      redirects: 0,
      timeout: '4s',
      tags: { name: 'RedirectHotPath' },
    });

    redirectDuration.add(res.timings.duration);
    const pass = check(res, {
      'redirect is 302': (r) => r.status === 302,
    });
    if (pass) successfulOps.add(1);

  } else {
    // =========================================================================
    // 5%: Dynamic URL Creations (POST /api/v1/urls - Postgres Write + Redis)
    // =========================================================================
    const slug = randomKey(8);
    const payload = JSON.stringify({
      original_url: 'https://go.dev/doc/k6-' + slug,
      title: 'k6 Benchmark Link ' + slug,
      custom_key: 's_' + slug,
    });

    headers['Content-Type'] = 'application/json';
    const res = http.post(BASE_URL + '/api/v1/urls', payload, {
      headers: headers,
      timeout: '4s',
      tags: { name: 'CreateShortURL' },
    });

    createDuration.add(res.timings.duration);
    const pass = check(res, {
      'create is 201': (r) => r.status === 201,
    });
    if (pass) successfulOps.add(1);
  }
}
