package db

import (
	"context"
	"database/sql"
	"fmt"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY,
    email VARCHAR(255) UNIQUE NOT NULL,
    api_key VARCHAR(64) UNIQUE NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

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

-- DuckLake PostgreSQL ACID Catalog Table for Tigris S3 Parquet partitions
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

-- Seed default user with deterministic UUIDv7 for public/unauthenticated short links
INSERT INTO users (id, email, api_key)
VALUES ('01927e00-0000-7000-8000-000000000001', 'admin@shortener.dev', 'default-api-key')
ON CONFLICT (id) DO NOTHING;
`

// RunMigrations executes initial table schema creation using direct *sql.DB.
func RunMigrations(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("failed to run postgres migrations: %w", err)
	}
	return nil
}
