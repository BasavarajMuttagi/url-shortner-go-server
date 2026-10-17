package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type urlCacheEntry struct {
	urlID       string
	originalURL string
	notFound    bool
}

// handleRedirect processes the visitor hot path:
// 1. Checks Redis cache (<1ms)
// 2. Uses singleflight.Group on cache miss to collapse concurrent stampedes into 1 DB query
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
		if err == nil {
			if val == "NOT_FOUND" {
				c.JSON(http.StatusNotFound, gin.H{"error": "Short URL not found or inactive"})
				return
			}
			parts := strings.SplitN(val, "|", 2)
			if len(parts) == 2 {
				urlID = parts[0]
				originalURL = parts[1]
			} else {
				originalURL = val
			}
		}
	}

	// 2. Slow path: Singleflight protected lookup on cache miss
	// Collapses concurrent requests for the same key into a single DB query, preventing cache stampedes
	if originalURL == "" {
		res, err, _ := s.sf.Do(shortKey, func() (interface{}, error) {
			// Double-check cache inside singleflight in case a concurrent call just populated it
			if s.rdb != nil {
				val, err := s.rdb.Get(context.Background(), "url:"+shortKey).Result()
				if err == nil {
					if val == "NOT_FOUND" {
						return urlCacheEntry{notFound: true}, nil
					}
					parts := strings.SplitN(val, "|", 2)
					if len(parts) == 2 {
						return urlCacheEntry{urlID: parts[0], originalURL: parts[1]}, nil
					}
				}
			}

			// Single DB query execution for all concurrent waiters
			var uID, origURL string
			query := `SELECT id, original_url FROM urls WHERE short_key = $1 AND is_active = true;`
			qErr := s.db.QueryRowContext(c.Request.Context(), query, shortKey).Scan(&uID, &origURL)
			if qErr != nil {
				if errors.Is(qErr, sql.ErrNoRows) {
					// Negative caching: cache NOT_FOUND for 2 minutes to prevent DB hammering
					if s.rdb != nil {
						_ = s.rdb.Set(context.Background(), "url:"+shortKey, "NOT_FOUND", 2*time.Minute).Err()
					}
					return urlCacheEntry{notFound: true}, nil
				}
				return nil, qErr
			}

			// Backfill Redis with randomized TTL jitter (+-10%) to prevent synchronized expirations
			if s.rdb != nil {
				jitterRange := int64(s.cfg.CacheTTL / 5) // 20% range (+-10%)
				var jitter time.Duration
				if jitterRange > 0 {
					jitter = time.Duration(rand.Int63n(jitterRange)) - (s.cfg.CacheTTL / 10)
				}
				actualTTL := s.cfg.CacheTTL + jitter
				_ = s.rdb.Set(context.Background(), "url:"+shortKey, fmt.Sprintf("%s|%s", uID, origURL), actualTTL).Err()
			}

			return urlCacheEntry{urlID: uID, originalURL: origURL}, nil
		})

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database lookup failed"})
			return
		}

		entry := res.(urlCacheEntry)
		if entry.notFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Short URL not found or inactive"})
			return
		}

		urlID = entry.urlID
		originalURL = entry.originalURL
	}

	// 3. Instant 302 Found response to visitor
	c.Redirect(http.StatusFound, originalURL)

	// 4. Non-blocking asynchronous analytics ingestion to DuckLake buffer
	if s.duckLake != nil {
		event := s.parser.Extract(c, urlID, shortKey)
		s.duckLake.Ingest(event)
	}
}
