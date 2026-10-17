# ⚡ Enterprise Grafana k6 Benchmark Suite

This directory contains the production-grade benchmark suite for the URL shortener service, implementing the **6 canonical named load testing types** used by enterprise performance engineering teams to test hardware utilization on cloud tiers:
- **Valkey (Redis)**: 1 vCPU / 1 GB RAM (Aiven Cloud)
- **PostgreSQL**: 1 vCPU / 1 GB RAM / 1 GB storage, tuned to utilize **18 of 20 maximum connections** with warm idle pools.

A single master file ([loadtest.js](file:///Users/basavarajmuttagi/Desktop/Scalable Backends/url-shortner/loadtest/loadtest.js)) tests every endpoint, exercises every architectural path, and maps system limits.

---

## 🏛️ The 6 Industry Standard Named Tests & What We Infer

| Named Test Type | What It Does (Traffic Pattern) | Why Companies Use It | What We Infer From The Results |
| :--- | :--- | :--- | :--- |
| **1. `smoke`** *(Smoke Test)* | 10 VUs for 15s. Minimal load across all routes. | Sanity check before committing code or running heavy benchmarks. | **Service Health**: Validates that DB migrations, Valkey cache, S3 analytics, and all HTTP handlers return expected status codes without panics. |
| **2. `load`** *(Average Load)* | Ramps to 1,000 VUs and holds for 2 minutes. | Simulates expected normal daytime peak traffic. | **Baseline SLA**: Verifies that under regular operations, response times easily satisfy $\text{p}95 < 50\text{ ms}$ with zero errors. |
| **3. `stress`** *(Stress Test)* | Stepped ladder: 500 $\rightarrow$ 1,500 $\rightarrow$ 3,000 $\rightarrow$ 5,000 VUs. | Tests behavior beyond expected capacity. | **Inflection Point**: Reveals where PostgreSQL connection queues or Valkey CPU start to saturate and where latency begins degrading. |
| **4. `spike`** *(Viral Event)* | Low baseline (100 VUs) $\rightarrow$ **explosive 3,000 VU surge in 10s** $\rightarrow$ abrupt drop $\rightarrow$ recovery. | Simulates a product going viral on Hacker News or Twitter. | **Shock Absorption & Recovery**: Confirms the 18-connection PostgreSQL pool absorbs sudden write waves without connection refused errors, and recovers immediately. |
| **5. `soak`** *(Endurance)* | Sustained 2,000 VUs held continuously for 6+ minutes. | Exposes slow degradation that short bursts hide. | **Leak Detection**: Verifies zero memory leaks on 1 GB RAM, no goroutine accumulation, and no unreturned database connections. |
| **6. `breakpoint`** *(Breaking Point)* | Continuous staircase ramp up to 12,000+ VUs until the server fails. | Capacity planning and scaling limits. | **Hardware Ceiling**: Mapped absolute maximum throughput that 1 CPU Valkey + 1 CPU Postgres can physically process. |

---

## 🔥 "What If The Product Went Viral?" — Workload Profiles

In real viral events, users don't just click links; thousands of users and automated bots simultaneously create and share new links. You can toggle the traffic distribution using `WORKLOAD`:

### 1. `WORKLOAD=viral` (Default — 50% Writes / 50% Reads)
* **45% Dynamic URL Creations (`POST /api/v1/urls`)**: Intense PostgreSQL ACID write pressure, keeping all 18 DB connections saturated and pre-warming Valkey.
* **45% Hot-Path Redirects (`GET /:shortKey`)**: Valkey in-memory $O(1)$ lookups + async analytics.
* **5% Metadata Lookups (`GET /api/v1/urls/:id`)**: Creators inspecting dashboard details.
* **3% 404 Penetration Attacks (`GET /missing-...`)**: Deflected by negative caching (`NOT_FOUND` cached 2m).
* **1% Chaos Cache Stampede Purges (`DELETE /api/v1/cache/:shortKey`)**: Validates `singleflight.Group` under peak write-and-read load.
* **1% Lakehouse Analytics (`GET /.../analytics`)**: DuckDB OLAP aggregations.

### 2. `WORKLOAD=balanced` (Standard Daytime)
75% Reads / 15% Writes / 5% Metadata / 3% Penetration / 1% Purge / 1% Analytics.

### 3. `WORKLOAD=readheavy` (Read-Dominated Scale)
90% Reads / 6% Writes / 2% Metadata / 2% Penetration.

---

## 🎮 Execution Modes

* **`MODE=users` (Default — Arjay McCandless Methodology)**:
  Simulates real human sessions with randomized think-times (`sleep 2–10s`). Each user generates $\sim 0.1\text{ to }0.15\text{ req/s}$, directly answering: *"How many concurrent users can our $12 server support?"*
* **`MODE=throughput`**:
  Open-loop machine-gun request bombardment with 0 think-time, testing maximum raw network/cache pipe saturation.

---

## 🚀 How to Run the Named Tests

```bash
# 1. Quick Smoke Test (Sanity check)
TEST_TYPE=smoke k6 run loadtest/loadtest.js

# 2. Viral Spike Test (Simulate going viral with 50% writes)
TEST_TYPE=spike WORKLOAD=viral k6 run loadtest/loadtest.js

# 3. Progressive Stress Test (Find capacity limits)
TEST_TYPE=stress WORKLOAD=viral k6 run loadtest/loadtest.js

# 4. Long Soak Test (6-minute memory leak detection)
TEST_TYPE=soak k6 run loadtest/loadtest.js

# 5. Breakpoint Test (Locate absolute breaking point)
TEST_TYPE=breakpoint k6 run loadtest/loadtest.js

# 6. Raw RPS Bombardment (Open-loop throughput test)
MODE=throughput TEST_TYPE=stress k6 run loadtest/loadtest.js

# 7. Remote Cloud Target
BASE_URL="http://your-server-ip:8080" TEST_TYPE=spike k6 run loadtest/loadtest.js
```

---

## 🎯 Enterprise Service Level Objectives (SLOs)

* **Global p95 Latency**: $< 500\text{ ms}$
* **Global p99 Latency**: $< 1,000\text{ ms}$ ($1\text{ second}$)
* **Operational Failure Rate**: $< 1.0\%$
* **Assertion Reliability**: $> 99.0\%$
