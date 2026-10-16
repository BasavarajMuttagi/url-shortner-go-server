package models

import (
	"time"

	"github.com/google/uuid"
)

// DefaultUserID is the deterministic UUIDv7 for the unauthenticated/default user account.
var DefaultUserID = uuid.MustParse("01927e00-0000-7000-8000-000000000001")

// User represents a link owner in PostgreSQL with a time-ordered UUIDv7 primary key.
type User struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	APIKey    string    `json:"api_key"`
	CreatedAt time.Time `json:"created_at"`
}

// URL represents a shortened URL mapping in PostgreSQL with a time-ordered UUIDv7 primary key.
type URL struct {
	ID          uuid.UUID `json:"id"`
	UserID      uuid.UUID `json:"user_id"`
	ShortKey    string    `json:"short_key"`
	OriginalURL string    `json:"original_url"`
	Title       string    `json:"title"`
	IsActive    bool      `json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ClickEvent represents an enriched click telemetry event.
type ClickEvent struct {
	URLID      string    `json:"url_id"`
	ShortKey   string    `json:"short_key"`
	Timestamp  time.Time `json:"timestamp"`
	IPHash     string    `json:"ip_hash"`
	Country    string    `json:"country"`
	City       string    `json:"city"`
	Browser    string    `json:"browser"`
	OS         string    `json:"os"`
	DeviceType string    `json:"device_type"`
	Referrer   string    `json:"referrer"`
}

// AnalyticsSummary contains aggregate metrics for a URL.
type AnalyticsSummary struct {
	TotalClicks    int64 `json:"total_clicks"`
	UniqueVisitors int64 `json:"unique_visitors"`
}

// TimeseriesPoint represents click volume in a time bucket.
type TimeseriesPoint struct {
	TimeBucket string `json:"time_bucket"`
	Clicks     int64  `json:"clicks"`
}

// DimensionCount represents categorical breakdown counts (Browser, OS, Country, etc.).
type DimensionCount struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

// AnalyticsReport is the complete analytical response payload for a URL.
type AnalyticsReport struct {
	URLID            string            `json:"url_id"`
	ShortKey         string            `json:"short_key"`
	TimeRange        string            `json:"time_range"`
	Summary          AnalyticsSummary  `json:"summary"`
	ClicksOverTime   []TimeseriesPoint `json:"clicks_over_time"`
	Browsers         []DimensionCount  `json:"browsers"`
	OperatingSystems []DimensionCount  `json:"operating_systems"`
	Devices          []DimensionCount  `json:"devices"`
	Referrers        []DimensionCount  `json:"referrers"`
	Countries        []DimensionCount  `json:"countries"`
}
