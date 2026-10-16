package helpers

import (
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const base62Chars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

// GenerateShortKey produces a cryptographically secure random base62 string of the given length
// using high-throughput uniform random bytes without heap big.Int allocations.
func GenerateShortKey(length int) string {
	const maxByte = 256 - (256 % len(base62Chars)) // 248 ensures unbiased modulo
	b := make([]byte, length)
	buf := make([]byte, length*2)
	idx := 0
	for idx < length {
		_, _ = rand.Read(buf)
		for _, val := range buf {
			if int(val) < maxByte {
				b[idx] = base62Chars[val%byte(len(base62Chars))]
				idx++
				if idx == length {
					break
				}
			}
		}
	}
	return string(b)
}

// GenerateBatchID creates a monotonically ordered, collision-free UUIDv7 batch identifier
// for Parquet files and DuckDB temp tables.
func GenerateBatchID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return strings.ReplaceAll(id.String(), "-", "")
}

// CalculateStartTime converts standard analytics time window filters (24h, 7d, 30d, all) into UTC start time.
func CalculateStartTime(timeRange string) time.Time {
	now := time.Now().UTC()
	switch strings.ToLower(timeRange) {
	case "24h":
		return now.Add(-24 * time.Hour)
	case "30d":
		return now.Add(-30 * 24 * time.Hour)
	case "all":
		return time.Unix(0, 0).UTC()
	case "7d":
		fallthrough
	default:
		return now.Add(-7 * 24 * time.Hour)
	}
}

// EscapeSQL sanitizes single quotes for safe string interpolation into DuckDB SQL statements.
func EscapeSQL(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
