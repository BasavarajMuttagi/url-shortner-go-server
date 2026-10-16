package helpers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestHeaderParser_Extract(t *testing.T) {
	parser := NewHeaderParser("test-salt-123")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	req, _ := http.NewRequest("GET", "/abc", nil)
	req.Header.Set("CF-Connecting-IP", "203.0.113.195")
	req.Header.Set("CF-IPCountry", "US")
	req.Header.Set("CF-IPCity", "San Francisco")
	req.Header.Set("Referer", "https://twitter.com/post/12345")
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 16_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.5 Mobile/15E148 Safari/604.1")
	c.Request = req

	testUUID := "01927e00-0000-7000-8000-000000000042"
	event := parser.Extract(c, testUUID, "abc")

	if event.URLID != testUUID {
		t.Errorf("expected URLID %s, got %s", testUUID, event.URLID)
	}
	if event.ShortKey != "abc" {
		t.Errorf("expected ShortKey 'abc', got '%s'", event.ShortKey)
	}
	if event.Country != "US" {
		t.Errorf("expected Country 'US', got '%s'", event.Country)
	}
	if event.City != "San Francisco" {
		t.Errorf("expected City 'San Francisco', got '%s'", event.City)
	}
	if event.Referrer != "twitter.com" {
		t.Errorf("expected Referrer 'twitter.com', got '%s'", event.Referrer)
	}
	if event.OS != "iOS" {
		t.Errorf("expected OS 'iOS', got '%s'", event.OS)
	}
	if event.Browser != "Safari" {
		t.Errorf("expected Browser 'Safari', got '%s'", event.Browser)
	}
	if event.DeviceType != "Mobile" {
		t.Errorf("expected DeviceType 'Mobile', got '%s'", event.DeviceType)
	}
	if event.IPHash == "" || event.IPHash == "unknown" {
		t.Errorf("expected valid non-empty IPHash, got '%s'", event.IPHash)
	}
}

func TestHeaderParser_BotDetection(t *testing.T) {
	parser := NewHeaderParser("test-salt-123")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	req, _ := http.NewRequest("GET", "/abc", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)")
	c.Request = req

	event := parser.Extract(c, "01927e00-0000-7000-8000-000000000001", "abc")

	if event.Browser != "Bot" {
		t.Errorf("expected Browser 'Bot', got '%s'", event.Browser)
	}
	if event.DeviceType != "Bot" {
		t.Errorf("expected DeviceType 'Bot', got '%s'", event.DeviceType)
	}
}
