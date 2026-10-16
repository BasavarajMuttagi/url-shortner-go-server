package helpers

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/google/uuid"
	"url-shortener/internal/config"
	"url-shortener/internal/models"
)

// DuckLakeEngine manages embedded DuckDB OLAP queries and Tigris S3 Parquet flushes.
type DuckLakeEngine struct {
	duckDB *sql.DB
	pgDB   *sql.DB
	mu     sync.RWMutex
	cfg    *config.Config
}

// NewDuckLakeEngine initializes the embedded DuckDB engine, loads httpfs,
// configures Tigris S3 credentials with vhost styling, and links to the PostgreSQL DuckLake catalog.
func NewDuckLakeEngine(ctx context.Context, cfg *config.Config, pgDB *sql.DB) (*DuckLakeEngine, error) {
	duckDB, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("failed to open embedded duckdb: %w", err)
	}
	duckDB.SetMaxOpenConns(1)

	engine := &DuckLakeEngine{
		duckDB: duckDB,
		pgDB:   pgDB,
		cfg:    cfg,
	}

	if err := engine.initDuckDB(ctx); err != nil {
		_ = duckDB.Close()
		return nil, err
	}

	return engine, nil
}

func (e *DuckLakeEngine) initDuckDB(ctx context.Context) error {
	// 1. Install & Load httpfs extension for S3 & HTTPS remote parquet scans
	if _, err := e.duckDB.ExecContext(ctx, "INSTALL httpfs; LOAD httpfs;"); err != nil {
		// Log and retry load if already installed
		if _, loadErr := e.duckDB.ExecContext(ctx, "LOAD httpfs;"); loadErr != nil {
			return fmt.Errorf("failed to load duckdb httpfs extension: %w", loadErr)
		}
	}

	// 2. Configure Tigris S3 credentials if provided
	if e.cfg.TigrisAccessKeyID != "" && e.cfg.TigrisSecretAccessKey != "" {
		secretQuery := fmt.Sprintf(`
			CREATE OR REPLACE SECRET tigris (
				TYPE s3,
				PROVIDER config,
				KEY_ID '%s',
				SECRET '%s',
				REGION '%s',
				ENDPOINT '%s',
				URL_STYLE 'vhost'
			);`,
			EscapeSQL(e.cfg.TigrisAccessKeyID),
			EscapeSQL(e.cfg.TigrisSecretAccessKey),
			EscapeSQL(e.cfg.TigrisRegion),
			EscapeSQL(e.cfg.TigrisEndpoint),
		)
		if _, err := e.duckDB.ExecContext(ctx, secretQuery); err != nil {
			return fmt.Errorf("failed to configure tigris secret in duckdb: %w", err)
		}
	}

	return nil
}

// FlushBatch writes a batch of click events to Tigris S3 as immutable columnar Parquet
// and records the partition metadata in the PostgreSQL DuckLake catalog.
func (e *DuckLakeEngine) FlushBatch(ctx context.Context, events []models.ClickEvent) error {
	if len(events) == 0 {
		return nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	batchID := GenerateBatchID()
	tempTable := fmt.Sprintf("temp_batch_%s", batchID)

	// Create temporary table in DuckDB for batch ingestion
	createTableSQL := fmt.Sprintf(`
		CREATE TEMP TABLE %s (
			url_id VARCHAR,
			short_key VARCHAR,
			timestamp TIMESTAMP,
			ip_hash VARCHAR,
			country VARCHAR,
			city VARCHAR,
			browser VARCHAR,
			os VARCHAR,
			device_type VARCHAR,
			referrer VARCHAR,
			partition_date DATE
		);
	`, tempTable)

	if _, err := e.duckDB.ExecContext(ctx, createTableSQL); err != nil {
		return fmt.Errorf("failed to create temp batch table: %w", err)
	}
	defer func() {
		_, _ = e.duckDB.ExecContext(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s;", tempTable))
	}()

	// Insert rows into temp table
	insertSQL := fmt.Sprintf(`
		INSERT INTO %s VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?::DATE);
	`, tempTable)

	stmt, err := e.duckDB.PrepareContext(ctx, insertSQL)
	if err != nil {
		return fmt.Errorf("failed to prepare batch insert: %w", err)
	}
	defer stmt.Close()

	for _, ev := range events {
		partitionDate := ev.Timestamp.Format("2006-01-02")
		_, err := stmt.ExecContext(ctx,
			ev.URLID,
			ev.ShortKey,
			ev.Timestamp,
			ev.IPHash,
			ev.Country,
			ev.City,
			ev.Browser,
			ev.OS,
			ev.DeviceType,
			ev.Referrer,
			partitionDate,
		)
		if err != nil {
			return fmt.Errorf("failed to insert event into temp batch: %w", err)
		}
	}

	// Query distinct (partition_date, url_id) groups
	groupsQuery := fmt.Sprintf(`
		SELECT 
			strftime(partition_date, '%%Y-%%m-%%d') AS pdate, 
			url_id, 
			count(*) AS cnt, 
			min(timestamp) AS min_ts, 
			max(timestamp) AS max_ts
		FROM %s
		GROUP BY 1, 2;
	`, tempTable)

	rows, err := e.duckDB.QueryContext(ctx, groupsQuery)
	if err != nil {
		return fmt.Errorf("failed to group temp batch: %w", err)
	}
	defer rows.Close()

	type groupInfo struct {
		partitionDate string
		urlID         string
		count         int64
		minTS         time.Time
		maxTS         time.Time
	}
	var groups []groupInfo

	for rows.Next() {
		var g groupInfo
		if err := rows.Scan(&g.partitionDate, &g.urlID, &g.count, &g.minTS, &g.maxTS); err != nil {
			return fmt.Errorf("failed to scan group info: %w", err)
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed reading grouped rows: %w", err)
	}

	// Flush each partition group to Tigris S3 as Parquet and update DuckLake catalog in Postgres
	for _, g := range groups {
		var s3Path string
		if e.cfg.TigrisAccessKeyID != "" {
			s3Path = fmt.Sprintf("s3://%s/analytics/date=%s/url_id=%s/batch_%s.parquet",
				e.cfg.TigrisBucket, g.partitionDate, g.urlID, batchID)
		} else {
			s3Path = fmt.Sprintf("./data/lake/analytics/date=%s/url_id=%s/batch_%s.parquet",
				g.partitionDate, g.urlID, batchID)
		}

		copySQL := fmt.Sprintf(`
			COPY (
				SELECT url_id, short_key, timestamp, ip_hash, country, city, browser, os, device_type, referrer
				FROM %s
				WHERE partition_date = '%s'::DATE AND url_id = '%s'
			) TO '%s' (FORMAT PARQUET, COMPRESSION ZSTD);
		`, tempTable, g.partitionDate, EscapeSQL(g.urlID), s3Path)

		if _, err := e.duckDB.ExecContext(ctx, copySQL); err != nil {
			return fmt.Errorf("failed to export parquet to tigris: %w", err)
		}

		// Update DuckLake catalog in PostgreSQL with time-ordered UUIDv7
		catalogID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("failed to generate catalog uuid: %w", err)
		}

		catalogInsertSQL := `
			INSERT INTO ducklake_catalog_files (
				id, url_id, partition_date, file_path, row_count, min_timestamp, max_timestamp, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8);
		`
		_, err = e.pgDB.ExecContext(ctx, catalogInsertSQL,
			catalogID,
			g.urlID,
			g.partitionDate,
			s3Path,
			g.count,
			g.minTS,
			g.maxTS,
			time.Now().UTC(),
		)
		if err != nil {
			return fmt.Errorf("failed to commit snapshot to ducklake catalog: %w", err)
		}
	}

	return nil
}

// GetAnalytics queries the DuckLake catalog in PostgreSQL for relevant Parquet partitions
// and executes vectorized aggregations over HTTPS via Tigris S3.
func (e *DuckLakeEngine) GetAnalytics(ctx context.Context, urlID string, shortKey, timeRange string) (*models.AnalyticsReport, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	startTime := CalculateStartTime(timeRange)

	report := &models.AnalyticsReport{
		URLID:            urlID,
		ShortKey:         shortKey,
		TimeRange:        timeRange,
		ClicksOverTime:   []models.TimeseriesPoint{},
		Browsers:         []models.DimensionCount{},
		OperatingSystems: []models.DimensionCount{},
		Devices:          []models.DimensionCount{},
		Referrers:        []models.DimensionCount{},
		Countries:        []models.DimensionCount{},
	}

	// 1. Read snapshot metadata from PostgreSQL DuckLake catalog to find target Parquet files
	catalogQuery := `
		SELECT file_path
		FROM ducklake_catalog_files
		WHERE url_id = $1 AND max_timestamp >= $2
		ORDER BY id ASC;
	`
	rows, err := e.pgDB.QueryContext(ctx, catalogQuery, urlID, startTime)
	if err != nil {
		return nil, fmt.Errorf("failed to query ducklake catalog metadata: %w", err)
	}
	defer rows.Close()

	var filePaths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err == nil {
			filePaths = append(filePaths, path)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading catalog rows: %w", err)
	}

	// If no files match the criteria, return empty report
	if len(filePaths) == 0 {
		return report, nil
	}

	// 2. Format files list and load into DuckDB in-memory temporary table in a single request
	quotedPaths := make([]string, len(filePaths))
	for i, p := range filePaths {
		quotedPaths[i] = fmt.Sprintf("'%s'", EscapeSQL(p))
	}
	parquetSource := fmt.Sprintf("read_parquet([%s])", strings.Join(quotedPaths, ", "))

	scanTable := fmt.Sprintf("scan_%s", GenerateBatchID())
	cacheSQL := fmt.Sprintf(`
		CREATE TEMP TABLE %s AS 
		SELECT * FROM %s 
		WHERE url_id = '%s' AND timestamp >= '%s';
	`, scanTable, parquetSource, EscapeSQL(urlID), startTime.Format("2006-01-02 15:04:05"))

	if _, err := e.duckDB.ExecContext(ctx, cacheSQL); err != nil {
		return nil, fmt.Errorf("failed to scan remote parquet files: %w", err)
	}
	defer func() {
		_, _ = e.duckDB.ExecContext(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s;", scanTable))
	}()

	// 3. Summary: Total clicks & unique visitors
	summaryQuery := fmt.Sprintf(`
		SELECT 
			COALESCE(count(*), 0) as total_clicks,
			COALESCE(count(DISTINCT ip_hash), 0) as unique_visitors
		FROM %s;
	`, scanTable)

	row := e.duckDB.QueryRowContext(ctx, summaryQuery)
	_ = row.Scan(&report.Summary.TotalClicks, &report.Summary.UniqueVisitors)

	// 4. Clicks over time (hourly buckets)
	timeseriesQuery := fmt.Sprintf(`
		SELECT 
			strftime(date_trunc('hour', timestamp), '%%Y-%%m-%%dT%%H:00:00Z') AS bucket,
			count(*) AS clicks
		FROM %s
		GROUP BY 1
		ORDER BY 1 ASC;
	`, scanTable)

	tsRows, err := e.duckDB.QueryContext(ctx, timeseriesQuery)
	if err == nil {
		defer tsRows.Close()
		for tsRows.Next() {
			var pt models.TimeseriesPoint
			if err := tsRows.Scan(&pt.TimeBucket, &pt.Clicks); err == nil {
				report.ClicksOverTime = append(report.ClicksOverTime, pt)
			}
		}
		if err := tsRows.Err(); err != nil {
			return nil, fmt.Errorf("failed reading timeseries rows: %w", err)
		}
	}

	// 5. Dimensional Breakdowns
	report.Browsers = e.queryDimension(ctx, scanTable, "browser")
	report.OperatingSystems = e.queryDimension(ctx, scanTable, "os")
	report.Devices = e.queryDimension(ctx, scanTable, "device_type")
	report.Referrers = e.queryDimension(ctx, scanTable, "referrer")
	report.Countries = e.queryDimension(ctx, scanTable, "country")

	return report, nil
}

func (e *DuckLakeEngine) queryDimension(ctx context.Context, table, column string) []models.DimensionCount {
	query := fmt.Sprintf(`
		SELECT COALESCE(%s, 'Unknown') as dim, count(*) as cnt
		FROM %s
		GROUP BY 1
		ORDER BY cnt DESC
		LIMIT 10;
	`, column, table)

	rows, err := e.duckDB.QueryContext(ctx, query)
	if err != nil {
		return []models.DimensionCount{}
	}
	defer rows.Close()

	var results []models.DimensionCount
	for rows.Next() {
		var item models.DimensionCount
		if err := rows.Scan(&item.Name, &item.Count); err == nil {
			results = append(results, item)
		}
	}
	if err := rows.Err(); err != nil {
		return []models.DimensionCount{}
	}
	return results
}

// Close closes the embedded DuckDB connection.
func (e *DuckLakeEngine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.duckDB != nil {
		return e.duckDB.Close()
	}
	return nil
}
