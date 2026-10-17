# ⚡ Scalable URL Shortener & DuckLake Analytics Engine

<p align="center">
  <b>A high-throughput, sub-3ms URL shortener & real-time columnar analytics lakehouse built in Go.</b><br>
  <i>Powered by Gin, Redis O(1) Cache, PostgreSQL (UUIDv7), Embedded DuckDB & Tigris S3 Parquet Storage</i>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go" alt="Go Version" />
  <img src="https://img.shields.io/badge/Throughput-13,500+%20req%2Fs-success?style=flat&logo=k6" alt="k6 Throughput" />
  <img src="https://img.shields.io/badge/Redirect%20Latency-2.47ms%20p50-blue?style=flat" alt="Latency" />
  <img src="https://img.shields.io/badge/Cache-Redis%20O(1)-DC382D?style=flat&logo=redis" alt="Redis" />
  <img src="https://img.shields.io/badge/Storage-PostgreSQL%20%2B%20S3-4169E1?style=flat&logo=postgresql" alt="Postgres" />
  <img src="https://img.shields.io/badge/OLAP-Embedded%20DuckDB-yellow?style=flat&logo=duckdb" alt="DuckDB" />
</p>

---

## Architecture Flow

```
Visitor ──> GET /:shortKey (Gin Engine)
                 │
                 ├─► 1. Lookup originalUrl (Redis / Postgres) ──► Instant 302 Found
                 │
                 └─► 2. Asynchronous Fire-and-Forget (Lightweight Go Goroutine)
                                │
                                ▼
                       Flush Event to Parquet ──► Upload to Tigris S3 (t3.storage.dev)
                                │
                                ▼
                       DuckLake updates catalog metadata in PostgreSQL

Link Owner ──> GET /api/v1/urls/:id/analytics (Gin Engine)
                     │
                     ▼
             Embedded DuckDB (go-duckdb)
                     │
                     ├─► Read snapshot metadata from PostgreSQL (DuckLake catalog)
                     └─► Scans & aggregates remote Parquet over HTTPS (httpfs) via Tigris S3
```

Detailed architectural specifications and component breakdowns can be found in [ARCHITECTURE.md](ARCHITECTURE.md).

---

## Project Structure (Next.js Style)

```text
url-shortener/
├── cmd/
│   └── server/
│       └── main.go           # 50-line clean application entrypoint
│
├── internal/
│   ├── api/                  # Like Next.js 'app/api/' - Pure HTTP route endpoints
│   │   ├── server.go         # Router initialization, CORS, & route binding
│   │   ├── redirect.go       # Route: GET /:shortKey (instant 302 redirect)
│   │   ├── url.go            # Route: /api/v1/urls (POST, GET, DELETE)
│   │   ├── analytics.go      # Route: GET /api/v1/urls/:id/analytics
│   │   └── redirect_test.go  # Route unit tests
│   │
│   ├── helpers/              # Like Next.js 'helpers/' or 'lib/' - Helper methods & engines
│   │   ├── ducklake.go       # DuckDB OLAP engine & Tigris S3 Parquet flusher
│   │   ├── parser.go         # User-Agent, Geo-IP, and IP hashing helpers
│   │   ├── utils.go          # Base62 key generation, timestamp & SQL helpers
│   │   └── parser_test.go    # Helper unit tests
│   │
│   ├── models/               # Like Next.js 'types/' - Domain data entities
│   │   └── models.go         # URL, User, ClickEvent, AnalyticsReport structs
│   │
│   ├── db/                   # PostgreSQL connection & migrations
│   │   ├── db.go             # Max 10 connections for Aiven Free tier (20 limit)
│   │   └── migrations.go     # ACID tables & default user seed
│   │
│   ├── cache/                # Redis connection client
│   │   └── redis.go          # Redis client initializer
│   │
│   └── config/               # Environment configuration
│       └── config.go         # Loads and validates .env
```

---

## Key Features

1. **Hot-Path Visitor Redirection (<5ms)**:
   - $O(1)$ in-memory lookups via Redis.
   - Instant HTTP `302 Found` redirection without waiting for database writes or network round-trips.
2. **Asynchronous Fire-and-Forget Telemetry**:
   - Telemetry dispatched asynchronously via lightweight Go goroutines.
   - Salted SHA-256 IP hashing provides GDPR-compliant unique visitor tracking.
3. **Columnar Parquet Storage on Tigris S3**:
   - Zero-egress distributed cloud object storage on `t3.storage.dev`.
   - Parquet files compressed with Zstandard (`ZSTD`) and partitioned by date and URL ID (`date=YYYY-MM-DD/url_id=...`).
4. **PostgreSQL DuckLake Catalog**:
   - Manages ACID transactions, snapshots, and file metadata in PostgreSQL.
   - Foreign key cascade (`ON DELETE CASCADE`) automatically cleans up catalog metadata when a URL is deleted.
5. **Embedded Vectorized Analytics (DuckDB)**:
   - In-process DuckDB queries remote Parquet over HTTPS via `httpfs`.
   - Aggregates clicks over time, unique visitors, browser, OS, device, referrer, and country distributions with SIMD execution.

---

## Configuration

Copy `.env.example` to `.env` and configure your credentials:

```bash
cp .env.example .env
```

| Variable | Description | Default |
| :--- | :--- | :--- |
| `PORT` | HTTP server port | `8080` |
| `BASE_URL` | Public base URL for shortened links | `http://localhost:8080` |
| `REDIS_URL` | Redis connection URL | `redis://localhost:6379` |
| `CACHE_TTL_HOURS` | Cache TTL duration in hours | `24` |
| `DATABASE_URL` | PostgreSQL connection URL | `postgres://...` |
| `AWS_ENDPOINT_URL_S3` | Tigris S3 endpoint | `https://t3.storage.dev` |
| `TIGRIS_BUCKET` | Tigris S3 target bucket | `shortener-analytics` |
| `AWS_ACCESS_KEY_ID` | Tigris S3 access key ID | `tid_...` |
| `AWS_SECRET_ACCESS_KEY` | Tigris S3 secret access key | `tsec_...` |
| `AWS_REGION` | Tigris S3 region | `auto` |
| `SALT_SECRET` | Salt used for GDPR IP hashing | Secret string |

---

## Building & Running

### 1. Run Tests
```bash
go test -v ./...
```

### 2. Build the Server Binary
```bash
go build -o server ./cmd/server
```

### 3. Start the Server
```bash
./server
```

---

## API Endpoints

### 1. Redirect Short URL
`GET /:shortKey`
- **Response**: `302 Found` with `Location` header.

### 2. Create Short URL
`POST /api/v1/urls`
- **Body**:
  ```json
  {
    "original_url": "https://example.com/blog/scalable-systems",
    "title": "Scalable Systems",
    "custom_key": "scalable"
  }
  ```
- **Response**: `201 Created`
  ```json
  {
    "id": "01a1458a-3a0b-71bd-9979-7f618847ee9a",
    "short_key": "scalable",
    "short_url": "http://localhost:8080/scalable",
    "original_url": "https://example.com/blog/scalable-systems",
    "title": "Scalable Systems",
    "created_at": "2026-10-16T16:50:00Z"
  }
  ```

### 3. Get URL Metadata
`GET /api/v1/urls/:id`
- **Example**: `GET /api/v1/urls/01a1458a-3a0b-71bd-9979-7f618847ee9a`
- **Response**: `200 OK`

### 4. List User URLs
`GET /api/v1/urls?user_id=01927e00-0000-7000-8000-000000000001&limit=20&offset=0`
- **Response**: `200 OK`

### 5. Delete Short URL
`DELETE /api/v1/urls/:id?user_id=01927e00-0000-7000-8000-000000000001`
- **Response**: `200 OK` (immediately invalidates Redis cache and cascades deletion)

### 6. Get URL Analytics
`GET /api/v1/urls/:id/analytics?range=7d`
- **Parameters**: `range=24h|7d|30d|all`
- **Response**: `200 OK`
  ```json
  {
    "url_id": "01a1458a-3a0b-71bd-9979-7f618847ee9a",
    "short_key": "scalable",
    "time_range": "7d",
    "summary": {
      "total_clicks": 14250,
      "unique_visitors": 9820
    },
    "clicks_over_time": [
      { "time_bucket": "2026-10-16T10:00:00Z", "clicks": 320 }
    ],
    "browsers": [
      { "name": "Chrome", "count": 8200 },
      { "name": "Safari", "count": 4100 }
    ],
    "operating_systems": [
      { "name": "macOS", "count": 6000 },
      { "name": "iOS", "count": 4000 }
    ],
    "devices": [
      { "name": "Desktop", "count": 8000 },
      { "name": "Mobile", "count": 5800 }
    ],
    "referrers": [
      { "name": "twitter.com", "count": 5200 },
      { "name": "direct", "count": 2400 }
    ],
    "countries": [
      { "name": "US", "count": 7000 },
      { "name": "IN", "count": 3500 }
    ]
  }
  ```

### 7. Health Check
`GET /health`
- **Response**: `200 OK`
  ```json
  {
    "status": "healthy",
    "engine": "gin",
    "olap": "duckdb"
  }
  ```

---

## Performance & Benchmarks (Grafana k6)

The service was benchmarked in a dedicated multi-machine cloud environment using the official **[Grafana k6 Standard Stress Testing Methodology](https://grafana.com/docs/k6/latest/testing-guides/stress-testing/)** located in [`loadtest/`](loadtest/).

### Workload Model (327,603 Total Requests)
* **Model**: Open-loop `ramping-arrival-rate` (up to 10,000 req/s).
* **Traffic Mix**: 95% Hot-Path Redis Redirects (`GET /:key`), 5% Dynamic Link Creations (`POST /api/v1/urls`).
* **4-Stage Lifecycle**: Warm-up (2.5k) ➔ Plateau (5k) ➔ Peak Stress (10k) ➔ Recovery (1k).

| Metric | Target SLA | Measured Result | Status |
| :--- | :---: | :---: | :---: |
| **Total Requests Completed** | — | **327,603** | ✅ Passed |
| **Success Rate (HTTP 302 / 201)** | `> 99.0%` | **100.00% (0 errors)** | 🚀 Flawless |
| **Hot-Path Redirect Latency (p50)** | `< 10 ms` | **2.47 ms** | ⚡ Sub-3ms |
| **Hot-Path Redirect Latency (p90)** | `< 25 ms` | **3.29 ms** | ⚡ Sub-5ms |
| **Hot-Path Redirect Latency (p95)** | `< 50 ms` | **5.16 ms** | ⚡ Sub-10ms |
| **Write Path DB Latency (p50)** | `< 50 ms` | **17.96 ms** | ✅ Optimal |
| **Peak Throughput Handled** | `> 5,000 req/s` | **13,510 req/s** | 🏆 Enterprise Grade |

To run the load tests against your own deployment, see [`loadtest/README.md`](loadtest/README.md).

