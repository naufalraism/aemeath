package aemeath_test

import (
	"fmt"
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

// One IPv6 client can use any address of its /64; rotating them used to give
// it a fresh limit for every request (1000 of 1000 allowed with limit 3).
func TestRateLimiterGroupsIPv6By64(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.RateLimiter(3, time.Minute))
	app.Handle("GET", "/", func(c *aemeath.Ctx) error { return c.Text(200, "ok") })
	hit := func(remote string) int {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w.Code
	}
	allowed := 0
	for i := 0; i < 100; i++ {
		if hit(fmt.Sprintf("[2001:db8:1:2::%x]:1234", i+1)) == 200 {
			allowed++
		}
	}
	if allowed != 3 {
		t.Fatalf("one /64 rotating addresses: %d allowed, want 3", allowed)
	}
	if hit("[2001:db8:1:3::1]:1234") != 200 {
		t.Fatal("a different /64 must have its own limit")
	}
	if hit("[::ffff:198.51.100.7]:1") != 200 || hit("198.51.100.7:2") != 200 || hit("198.51.100.7:3") != 200 || hit("198.51.100.7:4") != 429 {
		t.Fatal("an IPv4-mapped address must share the IPv4 address's limit")
	}
}
