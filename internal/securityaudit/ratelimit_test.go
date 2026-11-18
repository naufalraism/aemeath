package securityaudit

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
)

func rlApp(limit int) *aemeath.App {
	app := aemeath.New()
	app.Use(aemeath.RateLimiter(limit, time.Minute))
	app.Handle("GET", "/login", func(c *aemeath.Ctx) error { return c.Text(200, "ok") })
	return app
}

func hitFrom(app *aemeath.App, remote string) int {
	r := httptest.NewRequest("GET", "/login", nil)
	r.RemoteAddr = remote
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w.Code
}

// Sanity: one IPv4 address is limited.
func TestRateLimit_IPv4Limited(t *testing.T) {
	app := rlApp(3)
	codes := []int{}
	for i := 0; i < 5; i++ {
		codes = append(codes, hitFrom(app, "198.51.100.7:1234"))
	}
	t.Logf("5 requests from one IPv4: %v", codes)
	if codes[3] != 429 || codes[4] != 429 {
		t.Errorf("IPv4 client not limited: %v", codes)
	}
}

// Any IPv6 customer gets a /64 (18 quintillion addresses). One client that
// rotates its address inside its own /64 must still share one limit.
func TestRateLimit_IPv6RotationBypass(t *testing.T) {
	app := rlApp(3)
	allowed := 0
	for i := 0; i < 1000; i++ {
		if hitFrom(app, fmt.Sprintf("[2001:db8:1:2::%x]:1234", i+1)) == 200 {
			allowed++
		}
	}
	t.Logf("1000 requests from one IPv6 /64 with limit 3: %d allowed", allowed)
	if allowed > 3 {
		t.Errorf("WEAKNESS: limit 3 per client, but %d requests from the same /64 were allowed (brute force on /login is not slowed)", allowed)
	}
}
