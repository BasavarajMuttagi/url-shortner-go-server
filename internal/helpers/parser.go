package helpers

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"url-shortener/internal/models"
)

// HeaderParser extracts visitor telemetry from HTTP headers.
type HeaderParser struct {
	salt string
}

// NewHeaderParser creates a HeaderParser configured with the given IP hashing salt.
func NewHeaderParser(salt string) *HeaderParser {
	return &HeaderParser{salt: salt}
}

// Extract parses the Gin request into an enriched models.ClickEvent.
func (p *HeaderParser) Extract(c *gin.Context, urlID string, shortKey string) models.ClickEvent {
	ip := extractClientIP(c)
	country := extractCountry(c)
	city := extractCity(c)
	ua := c.GetHeader("User-Agent")
	browser, os, device := parseUserAgent(ua)
	referrer := sanitizeReferrer(c.GetHeader("Referer"))

	return NewClickEvent(urlID, shortKey, ip, p.salt, country, city, browser, os, device, referrer)
}

func extractClientIP(c *gin.Context) string {
	if ip := c.GetHeader("CF-Connecting-IP"); ip != "" {
		return strings.TrimSpace(ip)
	}
	if ip := c.GetHeader("X-Real-IP"); ip != "" {
		return strings.TrimSpace(ip)
	}
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
			return strings.TrimSpace(parts[0])
		}
	}
	return c.ClientIP()
}

func extractCountry(c *gin.Context) string {
	if country := c.GetHeader("CF-IPCountry"); country != "" && country != "XX" {
		return strings.ToUpper(strings.TrimSpace(country))
	}
	if country := c.GetHeader("X-Country-Code"); country != "" {
		return strings.ToUpper(strings.TrimSpace(country))
	}
	if country := c.GetHeader("GeoIP-Country-Code"); country != "" {
		return strings.ToUpper(strings.TrimSpace(country))
	}
	return "Unknown"
}

func extractCity(c *gin.Context) string {
	if city := c.GetHeader("CF-IPCity"); city != "" {
		return strings.TrimSpace(city)
	}
	if city := c.GetHeader("X-City"); city != "" {
		return strings.TrimSpace(city)
	}
	return "Unknown"
}

func sanitizeReferrer(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "Direct"
	}
	parsed, err := url.Parse(ref)
	if err != nil || parsed.Host == "" {
		return "Other"
	}
	host := strings.ToLower(parsed.Host)
	host = strings.TrimPrefix(host, "www.")
	return host
}

func parseUserAgent(ua string) (browser, os, deviceType string) {
	if ua == "" {
		return "Unknown", "Unknown", "Desktop"
	}

	lower := strings.ToLower(ua)

	// Bot detection using modern slices.ContainsFunc
	botKeywords := []string{"bot", "crawl", "spider", "slurp", "curl", "python", "wget"}
	if slices.ContainsFunc(botKeywords, func(kw string) bool { return strings.Contains(lower, kw) }) {
		return "Bot", "Server", "Bot"
	}

	// Device Type
	if strings.Contains(lower, "ipad") || strings.Contains(lower, "tablet") || strings.Contains(lower, "playbook") {
		deviceType = "Tablet"
	} else if strings.Contains(lower, "mobile") || strings.Contains(lower, "iphone") || strings.Contains(lower, "android") {
		deviceType = "Mobile"
	} else {
		deviceType = "Desktop"
	}

	// Operating System
	if strings.Contains(lower, "iphone") || strings.Contains(lower, "ipad") || strings.Contains(lower, "ipod") {
		os = "iOS"
	} else if strings.Contains(lower, "android") {
		os = "Android"
	} else if strings.Contains(lower, "mac os") || strings.Contains(lower, "macintosh") {
		os = "macOS"
	} else if strings.Contains(lower, "windows") {
		os = "Windows"
	} else if strings.Contains(lower, "linux") {
		os = "Linux"
	} else {
		os = "Other"
	}

	// Browser Detection
	if strings.Contains(lower, "edg/") || strings.Contains(lower, "edge") {
		browser = "Edge"
	} else if strings.Contains(lower, "opr/") || strings.Contains(lower, "opera") {
		browser = "Opera"
	} else if strings.Contains(lower, "chrome") && !strings.Contains(lower, "chromium") && !strings.Contains(lower, "edg") {
		browser = "Chrome"
	} else if strings.Contains(lower, "safari") && !strings.Contains(lower, "chrome") && !strings.Contains(lower, "android") {
		browser = "Safari"
	} else if strings.Contains(lower, "firefox") {
		browser = "Firefox"
	} else {
		browser = "Other"
	}

	return browser, os, deviceType
}

// HashIP generates a salted, irreversible SHA-256 hash of the client IP address.
func HashIP(ip, salt string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return "unknown"
	}
	h := sha256.New()
	h.Write([]byte(salt + ":" + ip))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// NewClickEvent constructs a models.ClickEvent with a current UTC timestamp.
func NewClickEvent(urlID string, shortKey, ip, salt, country, city, browser, os, deviceType, referrer string) models.ClickEvent {
	return models.ClickEvent{
		URLID:      urlID,
		ShortKey:   shortKey,
		Timestamp:  time.Now().UTC(),
		IPHash:     HashIP(ip, salt),
		Country:    country,
		City:       city,
		Browser:    browser,
		OS:         os,
		DeviceType: deviceType,
		Referrer:   referrer,
	}
}
