package api

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// getAnalytics handles GET /api/v1/urls/:id/analytics.
func (s *Server) getAnalytics(c *gin.Context) {
	idParam := c.Param("id")
	id, err := uuid.Parse(idParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid URL UUID format"})
		return
	}

	var shortKey string
	err = s.db.QueryRowContext(c.Request.Context(), "SELECT short_key FROM urls WHERE id = $1;", id).Scan(&shortKey)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "URL not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve URL record: " + err.Error()})
		return
	}

	timeRange := c.DefaultQuery("range", "7d")

	report, err := s.duckLake.GetAnalytics(c.Request.Context(), id.String(), shortKey, timeRange)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to compute analytics: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, report)
}
