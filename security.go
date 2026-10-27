package aemeath

import "net/http"

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

// MaxBodySize limits the size of request bodies globally (e.g. 2 MiB).
// Requests above the limit are rejected with 413 by typed routes.
func MaxBodySize(bytes int64) Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) error {
			c.R.Body = http.MaxBytesReader(c.W, c.R.Body, bytes)
			return next(c)
		}
	}
}
