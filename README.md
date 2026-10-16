# Ultra-Fast URL Shortener & Columnar Analytics Lakehouse

A high-performance URL shortener built with **Go (Gin)**, **Redis**, **PostgreSQL**, **Tigris S3** (`t3.storage.dev`), and an in-process **DuckLake + DuckDB** analytical data lakehouse. Structured with a clean **Next.js-style architecture** separating HTTP API routes from reusable helpers and storage engines.

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
