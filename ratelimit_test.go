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
	// Batasi 2 request per menit
	app.Use(aemeath.RateLimiter(2, 1*time.Minute))

	app.Handle("GET", "/ping", func(c *aemeath.Ctx) error {
		return c.JSON(200, map[string]string{"message": "pong"})
	})

	// Request 1 & 2 harus sukses
	for i := 1; i <= 2; i++ {
		req := httptest.NewRequest("GET", "/ping", nil)
		req.RemoteAddr = "192.168.1.1:1234" // Mock IP
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request ke-%d harusnya 200", i)
		}
	}

	// Request 3 harus diblokir (429)
	req := httptest.NewRequest("GET", "/ping", nil)
	req.RemoteAddr = "192.168.1.1:1234"
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("request ke-3 harusnya 429, dapat %d", rec.Code)
	}
}
