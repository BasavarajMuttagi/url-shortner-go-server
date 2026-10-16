package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds all service configurations.
type Config struct {
	// Server
	Port         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	BaseURL      string

	// Redis (Kept strictly as a URL)
	RedisURL string
	CacheTTL time.Duration

	// PostgreSQL (Kept strictly as a URL, no connection pooler)
	DatabaseURL string

	// Tigris S3 / Data Lake (Parts that form the S3 URL & DuckDB credentials)
	TigrisEndpoint        string
	TigrisBucket          string
	TigrisAccessKeyID     string
	TigrisSecretAccessKey string
	TigrisRegion          string

	// Ingestion & Batching
	BatchSize       int
	BatchInterval   time.Duration
	ChannelCapacity int
	WorkerPoolSize  int
	SaltSecret      string
}

// Load reads configuration from environment variables and .env file.
func Load() (*Config, error) {
	_ = godotenv.Load()

	// 1. Redis URL: Parse direct REDIS_URL, or build from Upstash REST URL/Token if provided
	redisURL := getEnv("REDIS_URL", "")
	if redisURL == "" {
		upstashRestURL := getEnv("UPSTASH_REDIS_REST_URL", "")
		upstashToken := getEnv("UPSTASH_REDIS_REST_TOKEN", "")
		if upstashRestURL != "" && upstashToken != "" {
			if parsed, err := url.Parse(upstashRestURL); err == nil {
				redisURL = fmt.Sprintf("rediss://default:%s@%s:6379", url.QueryEscape(upstashToken), parsed.Host)
			}
		}
	}
	if redisURL == "" {
		// Fallback for local Redis
		redisURL = "redis://localhost:6379"
	}

	// 2. Database URL: Direct PostgreSQL URL
	databaseURL := getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/shortener?sslmode=disable")

	// 3. Tigris S3 parts: kept as individual parts making up the S3 URL
	accessKey := getEnv("AWS_ACCESS_KEY_ID", getEnv("TIGRIS_ACCESS_KEY_ID", ""))
	secretKey := getEnv("AWS_SECRET_ACCESS_KEY", getEnv("TIGRIS_SECRET_ACCESS_KEY", ""))
	endpoint := getEnv("AWS_ENDPOINT_URL_S3", getEnv("TIGRIS_ENDPOINT", "t3.storage.dev"))
	endpoint = strings.TrimPrefix(endpoint, "https://")
	endpoint = strings.TrimPrefix(endpoint, "http://")
	endpoint = strings.TrimSuffix(endpoint, "/")

	region := getEnv("AWS_REGION", getEnv("TIGRIS_REGION", "auto"))
	bucket := getEnv("TIGRIS_BUCKET", "shortener-analytics")

	cfg := &Config{
		Port:                  getEnv("PORT", "8080"),
		ReadTimeout:           getDurationEnv("READ_TIMEOUT_SECONDS", 30) * time.Second,
		WriteTimeout:          getDurationEnv("WRITE_TIMEOUT_SECONDS", 30) * time.Second,
		BaseURL:               getEnv("BASE_URL", "http://localhost:8080"),
		RedisURL:              redisURL,
		CacheTTL:              getDurationEnv("CACHE_TTL_HOURS", 24) * time.Hour,
		DatabaseURL:           databaseURL,
		TigrisEndpoint:        endpoint,
		TigrisBucket:          bucket,
		TigrisAccessKeyID:     accessKey,
		TigrisSecretAccessKey: secretKey,
		TigrisRegion:          region,
		BatchSize:             getIntEnv("BATCH_SIZE", 1000),
		BatchInterval:         getDurationEnv("BATCH_INTERVAL_SECONDS", 5) * time.Second,
		ChannelCapacity:       getIntEnv("CHANNEL_CAPACITY", 10000),
		WorkerPoolSize:        getIntEnv("WORKER_POOL_SIZE", 4),
		SaltSecret:            getEnv("SALT_SECRET", "super-secret-salt-for-ip-hashing"),
	}

	return cfg, nil
}

// TigrisS3DataPath returns the S3 data path constructed from the bucket part.
func (c *Config) TigrisS3DataPath() string {
	if c.TigrisAccessKeyID == "" {
		return "./data/lake/analytics/"
	}
	return fmt.Sprintf("s3://%s/analytics/", c.TigrisBucket)
}

// TigrisEndpointURL returns the full HTTPS endpoint URL constructed from the endpoint part.
func (c *Config) TigrisEndpointURL() string {
	return fmt.Sprintf("https://%s", c.TigrisEndpoint)
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return defaultVal
}

func getIntEnv(key string, defaultVal int) int {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		if intVal, err := strconv.Atoi(val); err == nil {
			return intVal
		}
	}
	return defaultVal
}

func getDurationEnv(key string, defaultVal int) time.Duration {
	return time.Duration(getIntEnv(key, defaultVal))
}

// PrintSummary outputs sanitized config parameters for debugging.
func (c *Config) PrintSummary() {
	var maskedRedis string
	parts := strings.Split(c.RedisURL, "@")
	if len(parts) > 1 {
		maskedRedis = "rediss://***@" + parts[1]
	} else {
		maskedRedis = c.RedisURL
	}

	var maskedDB string
	dbParts := strings.Split(c.DatabaseURL, "@")
	if len(dbParts) > 1 {
		maskedDB = "postgres://***@" + dbParts[1]
	} else {
		maskedDB = c.DatabaseURL
	}

	fmt.Printf("[Config] Port: %s | BaseURL: %s\n", c.Port, c.BaseURL)
	fmt.Printf("[Config] Redis URL: %s\n", maskedRedis)
	fmt.Printf("[Config] Postgres URL: %s (direct connection, no pooler)\n", maskedDB)
	fmt.Printf("[Config] Tigris S3 Endpoint: %s | Bucket: %s | Region: %s\n", c.TigrisEndpoint, c.TigrisBucket, c.TigrisRegion)
	fmt.Printf("[Config] Batch Ingestion: %d events / %v interval\n", c.BatchSize, c.BatchInterval)
}
