package aemeath_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
)

func TestRateLimiter(t *testing.T) {
	app := aemeath.New()
	// Limit to 2 requests per minute
	app.Use(aemeath.RateLimiter(2, 1*time.Minute))

	app.Handle("GET", "/ping", func(c *aemeath.Ctx) error {
		return c.JSON(200, map[string]string{"message": "pong"})
	})

	// Requests 1 and 2 must succeed
	for i := 1; i <= 2; i++ {
		req := httptest.NewRequest("GET", "/ping", nil)
		req.RemoteAddr = "192.168.1.1:1234" // Mock IP
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d should be 200", i)
		}
	}

	// Request 3 must be blocked (429)
	req := httptest.NewRequest("GET", "/ping", nil)
	req.RemoteAddr = "192.168.1.1:1234"
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("request 3 should be 429, got %d", rec.Code)
	}
}
