package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"url-shortener/internal/models"
)

// handleRedirect processes the visitor hot path:
// 1. Checks Redis cache (<1ms)
// 2. Falls back to PostgreSQL on cache miss, populating Redis
// 3. Emits instant 302 Found
// 4. Fires a lightweight background goroutine to ingest the click into Tigris S3 via DuckLake
func (s *Server) handleRedirect(c *gin.Context) {
	shortKey := strings.Trim(strings.TrimSpace(c.Param("shortKey")), "/")
	if shortKey == "" || shortKey == "favicon.ico" || shortKey == "robots.txt" {
		c.Status(http.StatusNotFound)
		return
	}

	var (
		originalURL string
		urlID       string
	)

	// 1. Fast path: Redis direct lookup (<1ms)
	if s.rdb != nil {
		val, err := s.rdb.Get(c.Request.Context(), "url:"+shortKey).Result()
		if err == nil && val != "" {
			parts := strings.SplitN(val, "|", 2)
			if len(parts) == 2 {
				urlID = parts[0]
				originalURL = parts[1]
			} else {
				originalURL = val
			}
		}
	}

	// 2. Slow path: PostgreSQL lookup if cache miss
	if originalURL == "" {
		query := `SELECT id, original_url FROM urls WHERE short_key = $1 AND is_active = true;`
		err := s.db.QueryRowContext(c.Request.Context(), query, shortKey).Scan(&urlID, &originalURL)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				c.JSON(http.StatusNotFound, gin.H{"error": "Short URL not found or inactive"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database lookup failed"})
			return
		}

		// Asynchronously backfill Redis cache with detached context
		if s.rdb != nil {
			go func(key, id, origURL string) {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 2*time.Second)
				defer cancel()
				_ = s.rdb.Set(ctx, "url:"+key, fmt.Sprintf("%s|%s", id, origURL), s.cfg.CacheTTL).Err()
			}(shortKey, urlID, originalURL)
		}
	}

	// 3. Instant 302 Found response to visitor
	c.Redirect(http.StatusFound, originalURL)

	// 4. Background goroutine for analytics ingestion to Tigris S3 / DuckLake with detached context
	if s.duckLake != nil {
		event := s.parser.Extract(c, urlID, shortKey)
		go func(ev models.ClickEvent) {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 10*time.Second)
			defer cancel()
			_ = s.duckLake.FlushBatch(ctx, []models.ClickEvent{ev})
		}(event)
	}
}
