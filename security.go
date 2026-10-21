package aemeath

import "net/http"

// SecurityHeaders menambahkan header HTTP untuk melindungi browser pengguna.
func SecurityHeaders() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) error {
			// Mencegah Clickjacking (website lain membungkus API-mu di iframe)
			c.W.Header().Set("X-Frame-Options", "DENY")

			// Mencegah MIME-sniffing (browser menebak tipe file)
			c.W.Header().Set("X-Content-Type-Options", "nosniff")

			// Mengaktifkan proteksi XSS bawaan browser
			c.W.Header().Set("X-XSS-Protection", "1; mode=block")

			// Memaksa browser pakai HTTPS (jika di production)
			// c.W.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")

			return next(c)
		}
	}
}

// MaxBodySize membatasi ukuran body request secara global (misal: 2MB).
func MaxBodySize(bytes int64) Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) error {
			c.R.Body = http.MaxBytesReader(c.W, c.R.Body, bytes)
			return next(c)
		}
	}
}
