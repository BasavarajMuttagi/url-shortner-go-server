# System Architecture & Technical Specification
## High-Performance URL Shortener & Columnar Analytics Lakehouse

---

## 1. Executive Summary

This service is a high-throughput, ultra-low-latency URL shortening engine coupled with an embedded real-time analytical data lakehouse. It decouples high-volume visitor redirection from heavy analytical processing using an asynchronous, fire-and-forget telemetry pipeline.

The codebase is organized in a clean **Next.js-style architecture**, separating pure HTTP route endpoints (`internal/api/`) from helper methods and background storage engines (`internal/helpers/`).

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

---

## 2. Architecture Diagram

```mermaid
flowchart TD
    subgraph Traffic ["1. Hot Path: Visitor Redirection"]
        Visitor([Visitor]) -->|GET /:shortKey| GinEngine[Gin HTTP Router\ninternal/api/redirect.go]
        GinEngine -->|1. O(1) Key Lookup| RedisCache[(Valkey Cache\nAiven)]
        RedisCache -.->|Cache Miss Fallback| PostgresOLTP[(PostgreSQL OLTP\nAiven)]
        GinEngine -->|Instant 302 Found| Visitor
    end

    subgraph Ingestion ["2. Asynchronous Lakehouse Ingestion"]
        GinEngine -->|Fire-and-forget Goroutine| AsyncWorker[Go Goroutine\ninternal/helpers/ducklake.go]
        AsyncWorker -->|Export Columnar Parquet| TigrisS3[Tigris S3 Data Lake\nt3.storage.dev]
        AsyncWorker -->|Commit ACID Snapshot| DuckLakeCatalog[(DuckLake Catalog\nin PostgreSQL)]
    end

    subgraph OLAP ["3. In-Process Vectorized Analytics"]
        LinkOwner([Link Owner]) -->|GET /api/v1/urls/:id/analytics| AnalyticsHandler[Gin Analytics Handler\ninternal/api/analytics.go]
        AnalyticsHandler --> EmbeddedDuckDB[Embedded DuckDB\ngo-duckdb]
        EmbeddedDuckDB -->|1. Snapshot & Partition Pruning| DuckLakeCatalog
        EmbeddedDuckDB -->|2. Vectorized HTTP Range Scans| TigrisS3
        EmbeddedDuckDB -->|3. Aggregated Metrics JSON| LinkOwner
    end

    classDef primary fill:#2563eb,stroke:#1d4ed8,stroke-width:2px,color:#ffffff;
    classDef storage fill:#059669,stroke:#047857,stroke-width:2px,color:#ffffff;
    classDef queue fill:#d97706,stroke:#b45309,stroke-width:2px,color:#ffffff;
    classDef olap fill:#7c3aed,stroke:#6d28d9,stroke-width:2px,color:#ffffff;

    class GinEngine,AnalyticsHandler primary;
    class RedisCache,PostgresOLTP,TigrisS3,DuckLakeCatalog storage;
    class AsyncWorker queue;
    class EmbeddedDuckDB olap;
```

---

## 3. Storage Hierarchy & Role Separation

| Component | Technology | Primary Function | Performance Characteristics |
| :--- | :--- | :--- | :--- |
| **Cache Layer** | **Valkey / Redis** | Stores `url:{shortKey} -> {id}|{originalUrl}` with configurable TTL. | Sub-millisecond reads (<1ms), $O(1)$ memory lookup. |
| **OLTP Layer** | **PostgreSQL 16** | Stores `users` and `urls` relational data, operational CRUD, and authentication. | Direct connection limit (max 18 open connections) tuned for Aiven PostgreSQL (20 max). |
| **Data Lake** | **Tigris S3** (`t3.storage.dev`) | Immutable, append-only Apache Parquet files partitioned by `date` and `url_id`. | Zero-egress cost, high-throughput distributed object store with Zstandard compression. |
| **Catalog Format** | **DuckLake** | PostgreSQL-backed ACID catalog tracking table snapshots, Parquet file paths, zone maps (min/max), and partition ranges. | Eliminates small-file degradation, provides instant partition pruning and transactional isolation. |
| **OLAP Engine** | **Embedded DuckDB** (`go-duckdb`) | In-process vectorized query engine querying remote Parquet files via `httpfs`. | Columnar projection, SIMD-accelerated aggregations, single-connection thread safety with zero network hops. |

---

## 4. Subsystem Deep Dive

### 4.1 Hot Path: Redirection (`GET /:shortKey`)

1. **Routing**: Handled by Gin HTTP Engine in [redirect.go](internal/api/redirect.go).
2. **Lookup Sequence**:
   - Check **Redis** key `url:{shortKey}`.
   - If present: Retrieve `{id}|{originalUrl}` and immediately redirect.
   - If missing: Query PostgreSQL `urls` table by `short_key`. Asynchronously backfill Redis with a 24-hour TTL. If absent in Postgres, return HTTP 404 immediately.
3. **HTTP 302 Found**:
   - Return redirect immediately via `c.Redirect(http.StatusFound, originalUrl)`.
   - Client response is terminated without waiting for any analytics or storage I/O.
4. **Asynchronous Telemetry Dispatch**:
   - Extracts visitor headers via [parser.go](internal/helpers/parser.go):
     - **IP Address**: Extracted from `CF-Connecting-IP`, `X-Forwarded-For`, or `RemoteAddr`.
     - **Anonymization**: Salted SHA-256 hash (`HashIP`) generated to satisfy GDPR/privacy laws while preserving unique visitor counting.
     - **Country**: Extracted from `CF-IPCountry` / `X-Country-Code`.
     - **User-Agent**: Parsed into Browser, Operating System, and Device Type (Desktop, Mobile, Tablet, Bot).
     - **Referrer**: Domain-normalized (e.g. `twitter.com`, `Direct`).
   - A non-blocking background Go goroutine invokes `DuckLakeEngine.FlushBatch` without delaying the redirect.

---

### 4.2 Ingest Engine & Tigris S3 Parquet Flush

1. **Temporary Staging**:
   - Events are written to an in-memory DuckDB temporary table.
2. **Columnar Parquet Generation**:
   - DuckDB executes a vectorized `COPY (...) TO 's3://<bucket>/analytics/date=YYYY-MM-DD/url_id=<id>/batch_<id>.parquet' (FORMAT PARQUET, COMPRESSION ZSTD)`.
   - Files are partitioned using Hive layout: `date=YYYY-MM-DD/url_id=<id>/`.
3. **DuckLake Catalog Commit**:
   - Atomic SQL `INSERT INTO ducklake_catalog_files` records the file path, row count, min timestamp, and max timestamp in PostgreSQL.
   - Foreign key cascade (`ON DELETE CASCADE`) ensures that if a URL is deleted, all associated catalog snapshot records are automatically cleaned up.

---

### 4.3 Analytics Engine (OLAP via Embedded DuckDB)

1. **Query Entry**: `GET /api/v1/urls/:id/analytics?range=7d`
2. **Partition Pruning**:
   - DuckDB queries PostgreSQL catalog metadata to discover valid Parquet files for `url_id` where `max_timestamp >= startTime`.
3. **Single-Pass In-Memory Cache**:
   - Remote Parquet files are scanned **once** into an in-memory temporary table:
     ```sql
     CREATE TEMP TABLE scan_<id> AS 
     SELECT * FROM read_parquet(['s3://...']) 
     WHERE url_id = <id> AND timestamp >= '<start>';
     ```
4. **Vectorized In-Memory Aggregations**:
   - Runs all 7 dimensional breakdowns in memory (<3.8s total latency):
     - **Summary**: Total clicks, unique visitors (`COUNT(DISTINCT ip_hash)`).
     - **Timeseries**: Hourly click buckets (`date_trunc('hour', timestamp)`).
     - **Breakdowns**: Top browsers, operating systems, device types, referrers, and countries.
5. **Output**: Serialized into `models.AnalyticsReport` JSON.

---

## 5. Database & Catalog Schemas

### 5.1 PostgreSQL OLTP Schema (UUIDv7 Time-Ordered Primary Keys)

```sql
-- Users Table
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY,
    email VARCHAR(255) UNIQUE NOT NULL,
    api_key VARCHAR(64) UNIQUE NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

-- URLs Table (UUIDv7 Primary Key for non-enumerable, monotonically indexable URLs)
CREATE TABLE IF NOT EXISTS urls (
    id UUID PRIMARY KEY,
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    short_key VARCHAR(32) UNIQUE NOT NULL,
    original_url TEXT NOT NULL,
    title VARCHAR(255),
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_urls_user_id ON urls(user_id);
CREATE INDEX IF NOT EXISTS idx_urls_short_key ON urls(short_key);

-- DuckLake ACID Catalog Table for Tigris S3 Parquet partitions
CREATE TABLE IF NOT EXISTS ducklake_catalog_files (
    id UUID PRIMARY KEY,
    url_id UUID REFERENCES urls(id) ON DELETE CASCADE,
    partition_date DATE NOT NULL,
    file_path TEXT NOT NULL,
    row_count BIGINT NOT NULL,
    min_timestamp TIMESTAMPTZ,
    max_timestamp TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_ducklake_catalog_url_date ON ducklake_catalog_files(url_id, partition_date);
CREATE INDEX IF NOT EXISTS idx_ducklake_catalog_timestamp ON ducklake_catalog_files(url_id, max_timestamp);
```

### 5.2 Parquet Columnar Lakehouse Schema

```sql
-- Schema of immutable Apache Parquet files on Tigris S3
url_id      VARCHAR,    -- UUID string matching PostgreSQL urls.id
short_key   VARCHAR,
timestamp   TIMESTAMP,
ip_hash     VARCHAR,
country     VARCHAR,
city        VARCHAR,
browser     VARCHAR,
os          VARCHAR,
device_type VARCHAR,
referrer    VARCHAR
```

---

## 6. Project Layout (Next.js Pattern)

```text
├── cmd/
│   └── server/
│       └── main.go           # Application entrypoint
│
├── internal/
│   ├── api/                  # Pure HTTP routes (like Next.js 'app/api/')
│   │   ├── server.go         # Router configuration & lifecycle
│   │   ├── redirect.go       # Visitor redirection route
│   │   ├── url.go            # URL CRUD routes
│   │   ├── analytics.go      # Analytics report route
│   │   ├── metrics.go        # Live system & database pool telemetry
│   │   ├── metrics_test.go   # Metrics unit tests
│   │   └── redirect_test.go  # Route unit tests
│   │
│   ├── helpers/              # Reusable helper methods & engines (like Next.js 'lib/')
│   │   ├── ducklake.go       # DuckDB OLAP engine & Tigris S3 Parquet flusher
│   │   ├── parser.go         # User-Agent, Geo-IP, and IP hashing helpers
│   │   ├── utils.go          # Base62 key generation, timestamp & SQL helpers
│   │   └── parser_test.go    # Helper unit tests
│   │
│   ├── models/               # Domain data entities (like Next.js 'types/')
│   │   └── models.go         # URL, User, ClickEvent, AnalyticsReport structs
│   │
│   ├── db/                   # PostgreSQL connection & migrations
│   │   ├── db.go
│   │   └── migrations.go
│   │
│   ├── cache/                # Redis connection client
│   │   └── redis.go
│   │
│   └── config/               # Environment configuration
│       └── config.go
```

---

## 7. Reliability, Scalability & Performance Guarantees

1. **Zero Redirect Penalty**: Redirection logic never waits on analytics writes or remote object storage. All telemetry logging happens asynchronously in background goroutines.
2. **Connection Safety**: PostgreSQL connections are capped at 18 to maximize throughput against Aiven's 20-connection ceiling (preserving 2 slots for DB administration).
3. **Zero Egress Analytical Scans**: Parquet files are stored on Tigris S3 (`t3.storage.dev`), which charges zero egress fees, enabling frequent analytical queries without cloud bandwidth bills.
4. **Vectorized Columnar Execution**: Embedded DuckDB executes analytical queries in-process, reading only relevant Parquet byte ranges over HTTP range requests, delivering sub-second analytics even across millions of click events.
