package api

import (
	"math"
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
)

// SystemMetrics encapsulates runtime, memory, database pool, and cache status.
type SystemMetrics struct {
	Timestamp     time.Time        `json:"timestamp"`
	UptimeSeconds float64          `json:"uptime_seconds"`
	Runtime       RuntimeMetrics   `json:"runtime"`
	DatabasePool  DBPoolMetrics    `json:"database_pool"`
	Cache         CacheMetrics     `json:"cache"`
	Lakehouse     LakehouseMetrics `json:"lakehouse"`
}

type RuntimeMetrics struct {
	Goroutines    int     `json:"goroutines"`
	AllocMB       float64 `json:"alloc_mb"`
	TotalAllocMB  float64 `json:"total_alloc_mb"`
	SysMB         float64 `json:"sys_mb"`
	HeapInUseMB   float64 `json:"heap_in_use_mb"`
	NumGC         uint32  `json:"num_gc"`
	GCCPUFraction float64 `json:"gc_cpu_fraction"`
}

type DBPoolMetrics struct {
	MaxOpenConnections int   `json:"max_open_connections"`
	OpenConnections    int   `json:"open_connections"`
	InUse              int   `json:"in_use"`
	Idle               int   `json:"idle"`
	WaitCount          int64 `json:"wait_count"`
	WaitDurationMS     int64 `json:"wait_duration_ms"`
	MaxIdleClosed      int64 `json:"max_idle_closed"`
	MaxLifetimeClosed  int64 `json:"max_lifetime_closed"`
}

type CacheMetrics struct {
	Status     string `json:"status"`
	Hits       uint32 `json:"hits"`
	Misses     uint32 `json:"misses"`
	Timeouts   uint32 `json:"timeouts"`
	TotalConns uint32 `json:"total_conns"`
	IdleConns  uint32 `json:"idle_conns"`
	StaleConns uint32 `json:"stale_conns"`
}

type LakehouseMetrics struct {
	BufferedEvents int `json:"buffered_events"`
	BufferCapacity int `json:"buffer_capacity"`
}

// getMetrics handles GET /metrics and GET /api/v1/metrics.
func (s *Server) getMetrics(c *gin.Context) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	uptime := time.Since(s.startTime).Seconds()

	metrics := SystemMetrics{
		Timestamp:     time.Now().UTC(),
		UptimeSeconds: round2(uptime),
		Runtime: RuntimeMetrics{
			Goroutines:    runtime.NumGoroutine(),
			AllocMB:       round2(float64(m.Alloc) / (1024 * 1024)),
			TotalAllocMB:  round2(float64(m.TotalAlloc) / (1024 * 1024)),
			SysMB:         round2(float64(m.Sys) / (1024 * 1024)),
			HeapInUseMB:   round2(float64(m.HeapInuse) / (1024 * 1024)),
			NumGC:         m.NumGC,
			GCCPUFraction: round4(m.GCCPUFraction),
		},
	}

	if s.db != nil {
		stats := s.db.Stats()
		metrics.DatabasePool = DBPoolMetrics{
			MaxOpenConnections: stats.MaxOpenConnections,
			OpenConnections:    stats.OpenConnections,
			InUse:              stats.InUse,
			Idle:               stats.Idle,
			WaitCount:          stats.WaitCount,
			WaitDurationMS:     stats.WaitDuration.Milliseconds(),
			MaxIdleClosed:      stats.MaxIdleClosed,
			MaxLifetimeClosed:  stats.MaxLifetimeClosed,
		}
	}

	if s.rdb != nil {
		poolStats := s.rdb.PoolStats()
		metrics.Cache = CacheMetrics{
			Status:     "connected",
			Hits:       poolStats.Hits,
			Misses:     poolStats.Misses,
			Timeouts:   poolStats.Timeouts,
			TotalConns: poolStats.TotalConns,
			IdleConns:  poolStats.IdleConns,
			StaleConns: poolStats.StaleConns,
		}
	} else {
		metrics.Cache.Status = "disconnected"
	}

	if s.duckLake != nil {
		bufLen, bufCap := s.duckLake.BufferStats()
		metrics.Lakehouse = LakehouseMetrics{
			BufferedEvents: bufLen,
			BufferCapacity: bufCap,
		}
	}

	c.JSON(http.StatusOK, metrics)
}

func round2(val float64) float64 {
	return math.Round(val*100) / 100
}

func round4(val float64) float64 {
	return math.Round(val*10000) / 10000
}
