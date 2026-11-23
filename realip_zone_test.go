package aemeath_test

import (
	"io"
	"net"
	"net/http/httptest"
	"testing"

	"github.com/naufalraism/aemeath"
)

// An IPv6 zone ("%eth0") only means something on the local machine, never in
// a forwarded header. RealIP passed one through, and "::%[" became the
// RemoteAddr "[::%[]:4000", which net.SplitHostPort rejects (found by
// FuzzRealIP). Such entries are now treated like garbage.
func TestRealIPIgnoresAddressesWithAZone(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.RealIP("10.0.0.0/8"))
	app.Handle("GET", "/", func(c *aemeath.Ctx) error { _, err := io.WriteString(c.W, c.R.RemoteAddr); return err })
	for _, tc := range []struct{ xff, realIP, want string }{
		{"::%[", "", "10.0.0.5:4000"},
		{"fe80::1%eth0", "", "10.0.0.5:4000"},
		{"", "fe80::1%25eth0", "10.0.0.5:4000"},
		{"203.0.113.7, fe80::1%eth0", "", "10.0.0.5:4000"}, // garbage on the right: trust nothing further left
		{"fe80::1%eth0, 203.0.113.7", "", "203.0.113.7:4000"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "10.0.0.5:4000"
		if tc.xff != "" {
			r.Header.Set("X-Forwarded-For", tc.xff)
		}
		if tc.realIP != "" {
			r.Header.Set("X-Real-IP", tc.realIP)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		got := w.Body.String()
		if _, _, err := net.SplitHostPort(got); err != nil {
			t.Errorf("X-Forwarded-For %q, X-Real-IP %q: RemoteAddr %q is not host:port", tc.xff, tc.realIP, got)
		} else if got != tc.want {
			t.Errorf("X-Forwarded-For %q, X-Real-IP %q: RemoteAddr %q, want %q", tc.xff, tc.realIP, got, tc.want)
		}
	}
}
