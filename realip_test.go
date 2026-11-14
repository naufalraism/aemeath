package aemeath_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
)

func remoteSeen(t *testing.T, mw aemeath.Middleware, remote string, headers map[string]string) string {
	t.Helper()
	app := aemeath.New()
	app.Use(mw)
	var got string
	app.Handle("GET", "/", func(c *aemeath.Ctx) error { got = c.R.RemoteAddr; return nil })
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = remote
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	app.ServeHTTP(httptest.NewRecorder(), r)
	return got
}

func TestRealIP(t *testing.T) {
	mw := aemeath.RealIP(aemeath.PrivateNetworks...)
	cases := []struct {
		name, remote string
		headers      map[string]string
		want         string
	}{
		{"direct client keeps its address, header ignored", "203.0.113.7:5000",
			map[string]string{"X-Forwarded-For": "1.1.1.1"}, "203.0.113.7:5000"},
		{"proxy forwards the client", "10.0.0.2:4000",
			map[string]string{"X-Forwarded-For": "198.51.100.9"}, "198.51.100.9:4000"},
		{"client-forged hop on the left is skipped", "10.0.0.2:4000",
			map[string]string{"X-Forwarded-For": "6.6.6.6, 198.51.100.9"}, "198.51.100.9:4000"},
		{"several proxies", "10.0.0.2:4000",
			map[string]string{"X-Forwarded-For": "198.51.100.9, 10.1.1.1, 192.168.0.4"}, "198.51.100.9:4000"},
		{"X-Real-IP fallback", "127.0.0.1:4000",
			map[string]string{"X-Real-IP": "198.51.100.9"}, "198.51.100.9:4000"},
		{"IPv6 client behind an IPv6 proxy", "[fdaa::3]:4000",
			map[string]string{"X-Forwarded-For": "2001:db8::1"}, "[2001:db8::1]:4000"},
		{"garbage header leaves the address", "10.0.0.2:4000",
			map[string]string{"X-Forwarded-For": "not-an-ip"}, "10.0.0.2:4000"},
		{"no header leaves the address", "10.0.0.2:4000", nil, "10.0.0.2:4000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := remoteSeen(t, mw, tc.remote, tc.headers); got != tc.want {
				t.Fatalf("RemoteAddr = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRealIPSingleAddressAndBadInput(t *testing.T) {
	if got := remoteSeen(t, aemeath.RealIP("10.0.0.2"), "10.0.0.2:1", map[string]string{"X-Forwarded-For": "198.51.100.9"}); got != "198.51.100.9:1" {
		t.Fatalf("single trusted address: %q", got)
	}
	if got := remoteSeen(t, aemeath.RealIP("10.0.0.2"), "10.0.0.3:1", map[string]string{"X-Forwarded-For": "198.51.100.9"}); got != "10.0.0.3:1" {
		t.Fatalf("untrusted neighbour: %q", got)
	}
	for _, bad := range [][]string{{}, {"nope"}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("RealIP(%q) should panic", bad)
				}
			}()
			aemeath.RealIP(bad...)
		}()
	}
}

// Behind a proxy every request used to share the proxy's address, so one
// client used up the rate limit of everyone.
func TestRateLimiterBehindProxyWithRealIP(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.RealIP(aemeath.PrivateNetworks...), aemeath.RateLimiter(1, time.Minute))
	app.Handle("GET", "/", func(c *aemeath.Ctx) error { return c.Text(200, "ok") })
	hit := func(client string) int {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "10.0.0.2:4000" // the platform's proxy
		r.Header.Set("X-Forwarded-For", client)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w.Code
	}
	if hit("198.51.100.1") != 200 || hit("198.51.100.1") != 429 {
		t.Fatal("the same client should be limited")
	}
	if code := hit("198.51.100.2"); code != 200 {
		t.Fatalf("another client was limited too: %d", code)
	}
}
