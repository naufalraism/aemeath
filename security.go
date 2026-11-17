package aemeath

import (
	"io"
	"net/http"
)

// SecurityHeaders adds HTTP headers that harden the browser-facing surface.
func SecurityHeaders() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) error {
			h := c.W.Header()

			// Prevent clickjacking (other sites embedding the API in an iframe).
			h.Set("X-Frame-Options", "DENY")

			// Prevent MIME sniffing.
			h.Set("X-Content-Type-Options", "nosniff")

			// Disable the legacy XSS auditor: modern guidance is "0"
			// because the old "1; mode=block" mode can itself introduce leaks.
			h.Set("X-XSS-Protection", "0")

			// Do not leak URLs to other origins.
			h.Set("Referrer-Policy", "no-referrer")

			// Only send HSTS over a real TLS connection.
			if c.R.TLS != nil {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}

			return next(c)
		}
	}
}

// MaxBodySize sets the limit for request bodies (e.g. 2 MiB), in place of the
// default cap (see DefaultMaxRequestBytes); use it on a group or route to
// raise the cap for large uploads. A negative value removes the cap. Reading
// past the limit fails with *http.MaxBytesError, which becomes 413.
func MaxBodySize(bytes int64) Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) error {
			body := c.R.Body
			if d, ok := body.(*defaultBodyLimit); ok {
				body = d.orig
			}
			if bytes >= 0 {
				body = http.MaxBytesReader(c.W, body, bytes)
			}
			c.R.Body = body
			return next(c)
		}
	}
}

// DefaultMaxRequestBytes caps every request body, including the ones a
// handler reads itself from c.R.Body, so a client cannot stream gigabytes
// into a route that forgot a limit. 0 means the larger of DefaultMaxBodyBytes
// and DefaultMaxMultipartBytes (32 MiB by default); a negative value turns
// the cap off. MaxBodySize on a group or route replaces it. Typed routes keep
// their own, smaller limits.
var DefaultMaxRequestBytes int64

// defaultBodyLimit is the default cap, marked so MaxBodySize can replace it
// instead of nesting inside it.
type defaultBodyLimit struct {
	io.ReadCloser
	orig io.ReadCloser
}

func limitBody(w http.ResponseWriter, r *http.Request) {
	if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		return
	}
	n := DefaultMaxRequestBytes
	if n < 0 {
		return
	}
	if n == 0 {
		n = max(DefaultMaxBodyBytes, DefaultMaxMultipartBytes)
	}
	r.Body = &defaultBodyLimit{ReadCloser: http.MaxBytesReader(w, r.Body, n), orig: r.Body}
}
