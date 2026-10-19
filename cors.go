package aemeath

import (
	"net/http"
	"strconv"
	"strings"
)

// CORSConfig mengatur perilaku middleware CORS.
type CORSConfig struct {
	AllowOrigins     []string
	AllowMethods     []string
	AllowHeaders     []string
	AllowCredentials bool
	MaxAge           int
}

// DefaultCORSConfig memberikan pengaturan default yang aman.
func DefaultCORSConfig() CORSConfig {
	return CORSConfig{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Content-Type", "Authorization", "Accept"},
		AllowCredentials: false,
		MaxAge:           86400, // 24 jam
	}
}

// CORS adalah middleware untuk mengizinkan frontend (React/Vue) mengakses API.
func CORS(configs ...CORSConfig) Middleware {
	cfg := DefaultCORSConfig()
	if len(configs) > 0 {
		cfg = configs[0]
	}

	return func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) error {
			origin := c.R.Header.Get("Origin")
			if origin != "" {
				// Logika pengecekan origin
				allowOrigin := ""
				for _, o := range cfg.AllowOrigins {
					if o == "*" || o == origin {
						allowOrigin = o
						break
					}
				}

				if allowOrigin != "" {
					if allowOrigin == "*" {
						c.W.Header().Set("Access-Control-Allow-Origin", "*")
					} else {
						c.W.Header().Set("Access-Control-Allow-Origin", origin)
						c.W.Header().Add("Vary", "Origin")
					}

					if cfg.AllowCredentials {
						c.W.Header().Set("Access-Control-Allow-Credentials", "true")
					}

					// Handle Preflight Request (OPTIONS)
					if c.R.Method == http.MethodOptions {
						c.W.Header().Set("Access-Control-Allow-Methods", strings.Join(cfg.AllowMethods, ", "))
						c.W.Header().Set("Access-Control-Allow-Headers", strings.Join(cfg.AllowHeaders, ", "))
						if cfg.MaxAge > 0 {
							c.W.Header().Set("Access-Control-Max-Age", strconv.Itoa(cfg.MaxAge))
						}
						return c.JSON(http.StatusNoContent, nil)
					}
				}
			}
			return next(c)
		}
	}
}
