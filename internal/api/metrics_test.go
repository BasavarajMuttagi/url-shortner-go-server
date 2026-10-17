package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"url-shortener/internal/config"
	"url-shortener/internal/helpers"
)

func TestMetricsEndpoint(t *testing.T) {
	testDB := setupTestDB(t)
	defer testDB.Close()

	cfg := &config.Config{
		BaseURL:    "http://localhost:8080",
		CacheTTL:   1 * time.Hour,
		SaltSecret: "test-salt",
	}

	router := gin.New()
	s := &Server{
		router:    router,
		cfg:       cfg,
		db:        testDB,
		rdb:       nil,
		duckLake:  nil,
		parser:    helpers.NewHeaderParser(cfg.SaltSecret),
		startTime: time.Now().Add(-10 * time.Second),
	}
	s.setupRoutes()

	// 1. Test GET /metrics
	req, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 for /metrics, got %d", w.Code)
	}

	var res SystemMetrics
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode metrics JSON: %v", err)
	}

	if res.UptimeSeconds < 9.0 {
		t.Errorf("expected uptime >= 9.0s, got %f", res.UptimeSeconds)
	}

	if res.Runtime.Goroutines <= 0 {
		t.Errorf("expected goroutines > 0, got %d", res.Runtime.Goroutines)
	}

	if res.Runtime.AllocMB <= 0 {
		t.Errorf("expected alloc_mb > 0, got %f", res.Runtime.AllocMB)
	}

	if res.Cache.Status != "disconnected" {
		t.Errorf("expected cache status 'disconnected' when nil, got %s", res.Cache.Status)
	}

	// 2. Test GET /api/v1/metrics
	req2, _ := http.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	w2 := httptest.NewRecorder()
	s.router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected status 200 for /api/v1/metrics, got %d", w2.Code)
	}
}
