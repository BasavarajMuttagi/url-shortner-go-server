package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Connect initializes the PostgreSQL connection with direct connection limits.
func Connect(ctx context.Context, databaseURL string) (*sql.DB, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres connection: %w", err)
	}

	// Maximize throughput against Aiven PostgreSQL's 20-connection limit.
	// Allocating 18 active connections provides 80% higher write capacity than default 10,
	// while reserving 2 connections for psql administration/migrations.
	// Setting MaxIdleConns = MaxOpenConns prevents connection re-negotiation latency under bursts.
	db.SetMaxOpenConns(18)
	db.SetMaxIdleConns(18)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping postgres: %w", err)
	}

	return db, nil
}
