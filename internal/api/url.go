package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"url-shortener/internal/helpers"
	"url-shortener/internal/models"
)

// CreateURLRequest represents the payload to shorten a URL.
type CreateURLRequest struct {
	OriginalURL string `json:"original_url" binding:"required"`
	Title       string `json:"title"`
	CustomKey   string `json:"custom_key"`
	UserID      string `json:"user_id,omitempty"`
}

// CreateURLResponse returns the created URL metadata with a UUIDv7 primary key.
type CreateURLResponse struct {
	ID          uuid.UUID `json:"id"`
	ShortKey    string    `json:"short_key"`
	ShortURL    string    `json:"short_url"`
	OriginalURL string    `json:"original_url"`
	Title       string    `json:"title"`
	CreatedAt   time.Time `json:"created_at"`
}

// createURL handles POST /api/v1/urls.
func (s *Server) createURL(c *gin.Context) {
	var req CreateURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload: " + err.Error()})
		return
	}

	req.OriginalURL = strings.TrimSpace(req.OriginalURL)
	parsedURL, err := url.ParseRequestURI(req.OriginalURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Valid http or https URL is required"})
		return
	}

	shortKey := strings.TrimSpace(req.CustomKey)
	if shortKey == "" {
		shortKey = helpers.GenerateShortKey(7)
	}

	userID := models.DefaultUserID
	if strings.TrimSpace(req.UserID) != "" {
		parsedUID, err := uuid.Parse(req.UserID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user_id UUID format"})
			return
		}
		userID = parsedUID
	}

	// Generate time-ordered UUIDv7 for sequential database indexing
	newID, err := uuid.NewV7()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate URL UUID: " + err.Error()})
		return
	}

	now := time.Now().UTC()
	var urlRecord models.URL
	query := `
		INSERT INTO urls (id, user_id, short_key, original_url, title, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, user_id, short_key, original_url, title, is_active, created_at, updated_at;
	`
	err = s.db.QueryRowContext(c.Request.Context(), query,
		newID,
		userID,
		shortKey,
		req.OriginalURL,
		req.Title,
		true,
		now,
		now,
	).Scan(
		&urlRecord.ID,
		&urlRecord.UserID,
		&urlRecord.ShortKey,
		&urlRecord.OriginalURL,
		&urlRecord.Title,
		&urlRecord.IsActive,
		&urlRecord.CreatedAt,
		&urlRecord.UpdatedAt,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create short URL: " + err.Error()})
		return
	}

	// Warm cache in Redis immediately with both ID (UUID) and original URL
	if s.rdb != nil {
		val := fmt.Sprintf("%s|%s", urlRecord.ID.String(), urlRecord.OriginalURL)
		_ = s.rdb.Set(c.Request.Context(), "url:"+urlRecord.ShortKey, val, s.cfg.CacheTTL).Err()
	}

	c.JSON(http.StatusCreated, CreateURLResponse{
		ID:          urlRecord.ID,
		ShortKey:    urlRecord.ShortKey,
		ShortURL:    strings.TrimSuffix(s.cfg.BaseURL, "/") + "/" + urlRecord.ShortKey,
		OriginalURL: urlRecord.OriginalURL,
		Title:       urlRecord.Title,
		CreatedAt:   urlRecord.CreatedAt,
	})
}

// getURL handles GET /api/v1/urls/:id.
func (s *Server) getURL(c *gin.Context) {
	idParam := c.Param("id")
	id, err := uuid.Parse(idParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid URL UUID format"})
		return
	}

	query := `
		SELECT id, user_id, short_key, original_url, title, is_active, created_at, updated_at
		FROM urls
		WHERE id = $1;
	`
	var record models.URL
	err = s.db.QueryRowContext(c.Request.Context(), query, id).Scan(
		&record.ID,
		&record.UserID,
		&record.ShortKey,
		&record.OriginalURL,
		&record.Title,
		&record.IsActive,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "URL not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database lookup failed: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":           record.ID,
		"user_id":      record.UserID,
		"short_key":    record.ShortKey,
		"short_url":    strings.TrimSuffix(s.cfg.BaseURL, "/") + "/" + record.ShortKey,
		"original_url": record.OriginalURL,
		"title":        record.Title,
		"is_active":    record.IsActive,
		"created_at":   record.CreatedAt,
	})
}

// listURLs handles GET /api/v1/urls.
func (s *Server) listURLs(c *gin.Context) {
	userIDStr := c.DefaultQuery("user_id", models.DefaultUserID.String())
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user_id UUID format"})
		return
	}

	limitStr := c.DefaultQuery("limit", "20")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	offsetStr := c.DefaultQuery("offset", "0")
	offset, _ := strconv.Atoi(offsetStr)
	if offset < 0 {
		offset = 0
	}

	query := `
		SELECT id, user_id, short_key, original_url, title, is_active, created_at, updated_at
		FROM urls
		WHERE user_id = $1
		ORDER BY id DESC
		LIMIT $2 OFFSET $3;
	`
	rows, err := s.db.QueryContext(c.Request.Context(), query, userID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list URLs: " + err.Error()})
		return
	}
	defer rows.Close()

	var urls []models.URL
	for rows.Next() {
		var u models.URL
		if err := rows.Scan(
			&u.ID,
			&u.UserID,
			&u.ShortKey,
			&u.OriginalURL,
			&u.Title,
			&u.IsActive,
			&u.CreatedAt,
			&u.UpdatedAt,
		); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to scan URL row: " + err.Error()})
			return
		}
		urls = append(urls, u)
	}
	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed reading URL rows: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"urls":   urls,
		"limit":  limit,
		"offset": offset,
	})
}

// deleteURL handles DELETE /api/v1/urls/:id.
func (s *Server) deleteURL(c *gin.Context) {
	idParam := c.Param("id")
	id, err := uuid.Parse(idParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid URL UUID format"})
		return
	}

	userIDStr := c.DefaultQuery("user_id", models.DefaultUserID.String())
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user_id UUID format"})
		return
	}

	var shortKey string
	query := `DELETE FROM urls WHERE id = $1 AND user_id = $2 RETURNING short_key;`
	err = s.db.QueryRowContext(c.Request.Context(), query, id, userID).Scan(&shortKey)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "URL not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete URL: " + err.Error()})
		return
	}

	// Invalidate cache immediately in Redis
	if s.rdb != nil {
		_ = s.rdb.Del(c.Request.Context(), "url:"+shortKey).Err()
	}

	c.JSON(http.StatusOK, gin.H{
		"status":    "deleted",
		"id":        id,
		"short_key": shortKey,
	})
}
