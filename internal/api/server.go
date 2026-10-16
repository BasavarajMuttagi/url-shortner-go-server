package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"url-shortener/internal/config"
	"url-shortener/internal/helpers"
)

// Server encapsulates the HTTP router, database connections, and Lakehouse analytics.
type Server struct {
	router   *gin.Engine
	cfg      *config.Config
	db       *sql.DB
	rdb      *redis.Client
	duckLake *helpers.DuckLakeEngine
	parser   *helpers.HeaderParser
}

// NewServer builds and configures the Gin engine with direct DB, Redis, and DuckLake engine.
func NewServer(
	ctx context.Context,
	cfg *config.Config,
	db *sql.DB,
	rdb *redis.Client,
) (*Server, error) {
	duckLake, err := helpers.NewDuckLakeEngine(ctx, cfg, db)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize ducklake engine: %w", err)
	}

	parser := helpers.NewHeaderParser(cfg.SaltSecret)

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()

	// Essential middleware
	router.Use(gin.Recovery())
	router.Use(gin.Logger())

	// CORS support
	router.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	})

	s := &Server{
		router:   router,
		cfg:      cfg,
		db:       db,
		rdb:      rdb,
		duckLake: duckLake,
		parser:   parser,
	}

	s.setupRoutes()
	return s, nil
}

func (s *Server) setupRoutes() {
	// Health check
	s.router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "healthy",
			"engine": "gin",
			"olap":   "duckdb",
		})
	})

	// API v1
	v1 := s.router.Group("/api/v1")
	{
		// URL CRUD
		v1.POST("/urls", s.createURL)
		v1.GET("/urls/:id", s.getURL)
		v1.GET("/urls", s.listURLs)
		v1.DELETE("/urls/:id", s.deleteURL)

		// Analytics
		v1.GET("/urls/:id/analytics", s.getAnalytics)
	}

	// Visitor Hot Path (Instant 302 Found)
	s.router.GET("/:shortKey", s.handleRedirect)
}

// Router returns the configured Gin router.
func (s *Server) Router() *gin.Engine {
	return s.router
}

// Close gracefully closes the embedded DuckDB connection.
func (s *Server) Close() error {
	if s.duckLake != nil {
		return s.duckLake.Close()
	}
	return nil
}
