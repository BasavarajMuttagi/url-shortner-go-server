package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/marcboeker/go-duckdb"
	"url-shortener/internal/config"
	"url-shortener/internal/helpers"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("failed to open in-memory test db: %v", err)
	}

	createSQL := `
		CREATE TABLE urls (
			id VARCHAR,
			user_id VARCHAR,
			short_key VARCHAR,
			original_url VARCHAR,
			title VARCHAR,
			is_active BOOLEAN,
			created_at TIMESTAMP,
			updated_at TIMESTAMP
		);
	`
	if _, err := db.Exec(createSQL); err != nil {
		t.Fatalf("failed to create test schema: %v", err)
	}

	insertSQL := `
		INSERT INTO urls VALUES ('01927e00-0000-7000-8000-000000000042', '01927e00-0000-7000-8000-000000000001', 'testKey', 'https://golang.org', 'Go', true, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
	`
	if _, err := db.Exec(insertSQL); err != nil {
		t.Fatalf("failed to insert test url: %v", err)
	}

	return db
}

func TestRedirect_Found(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	cfg := &config.Config{BaseURL: "http://localhost:8080", CacheTTL: 24 * time.Hour}
	server := &Server{
		cfg:    cfg,
		db:     db,
		parser: helpers.NewHeaderParser("salt"),
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", "/testKey", nil)
	c.Params = gin.Params{{Key: "shortKey", Value: "testKey"}}
	c.Request = req

	server.handleRedirect(c)

	if w.Code != http.StatusFound {
		t.Fatalf("expected status 302 Found, got %d", w.Code)
	}

	loc := w.Header().Get("Location")
	if loc != "https://golang.org" {
		t.Fatalf("expected redirect to https://golang.org, got %s", loc)
	}
}

func TestRedirect_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	cfg := &config.Config{BaseURL: "http://localhost:8080", CacheTTL: 24 * time.Hour}
	server := &Server{
		cfg:    cfg,
		db:     db,
		parser: helpers.NewHeaderParser("salt"),
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", "/nonexistent", nil)
	c.Params = gin.Params{{Key: "shortKey", Value: "nonexistent"}}
	c.Request = req

	server.handleRedirect(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 Not Found, got %d", w.Code)
	}
}

func TestRedirect_CacheStampede_Singleflight(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	cfg := &config.Config{BaseURL: "http://localhost:8080", CacheTTL: 24 * time.Hour}
	server := &Server{
		cfg:    cfg,
		db:     db,
		parser: helpers.NewHeaderParser("salt"),
	}

	const concurrentReqs = 50
	errChan := make(chan error, concurrentReqs)

	for i := 0; i < concurrentReqs; i++ {
		go func() {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			req, _ := http.NewRequest("GET", "/testKey", nil)
			c.Params = gin.Params{{Key: "shortKey", Value: "testKey"}}
			c.Request = req

			server.handleRedirect(c)

			if w.Code != http.StatusFound {
				errChan <- fmt.Errorf("expected 302 Found, got %d", w.Code)
				return
			}
			loc := w.Header().Get("Location")
			if loc != "https://golang.org" {
				errChan <- fmt.Errorf("expected redirect to https://golang.org, got %s", loc)
				return
			}
			errChan <- nil
		}()
	}

	for i := 0; i < concurrentReqs; i++ {
		if err := <-errChan; err != nil {
			t.Fatalf("concurrent redirect failed: %v", err)
		}
	}
}
