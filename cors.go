package aemeath

import (
	"net/http"
	"strconv"
	"strings"
)

// CORSConfig configures the CORS middleware.
type CORSConfig struct {
	AllowOrigins     []string
	AllowMethods     []string
	AllowHeaders     []string
	ExposeHeaders    []string
	AllowCredentials bool
	MaxAge           int
}

// DefaultCORSConfig returns a permissive configuration suitable for public
// APIs and local development: any origin, no credentials.
func DefaultCORSConfig() CORSConfig {
	return CORSConfig{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Content-Type", "Authorization", "Accept"},
		AllowCredentials: false,
		MaxAge:           86400, // 24 hours
	}
}

// CORS allows browser frontends (React, Vue, ...) to call the API.
//
// Empty AllowMethods and AllowHeaders fall back to the defaults. Combining
// AllowCredentials with the "*" origin is rejected by browsers and unsafe to
// emulate, so it panics at startup: list the allowed origins explicitly.
func CORS(configs ...CORSConfig) Middleware {
	cfg := DefaultCORSConfig()
	if len(configs) > 0 {
		def := DefaultCORSConfig()
		cfg = configs[0]
		if len(cfg.AllowMethods) == 0 {
			cfg.AllowMethods = def.AllowMethods
		}
		if len(cfg.AllowHeaders) == 0 {
			cfg.AllowHeaders = def.AllowHeaders
		}
	}
	if cfg.AllowCredentials {
		for _, o := range cfg.AllowOrigins {
			if o == "*" {
				panic("aemeath: CORS: AllowCredentials cannot be combined with the \"*\" origin; list the allowed origins explicitly")
			}
		}
	}

	return func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) error {
			origin := c.R.Header.Get("Origin")
			if origin == "" {
				return next(c)
			}

			allowOrigin := ""
			for _, o := range cfg.AllowOrigins {
				if o == "*" {
					allowOrigin = "*"
					break
				}
				if o == origin {
					allowOrigin = origin
					break
				}
			}
			if allowOrigin == "" {
				return next(c)
			}

			h := c.W.Header()
			h.Set("Access-Control-Allow-Origin", allowOrigin)
			if allowOrigin != "*" {
				h.Add("Vary", "Origin")
			}
			if cfg.AllowCredentials {
				h.Set("Access-Control-Allow-Credentials", "true")
			}
			if len(cfg.ExposeHeaders) > 0 {
				h.Set("Access-Control-Expose-Headers", strings.Join(cfg.ExposeHeaders, ", "))
			}

			// Preflight request (OPTIONS)
			if c.R.Method == http.MethodOptions {
				h.Set("Access-Control-Allow-Methods", strings.Join(cfg.AllowMethods, ", "))
				h.Set("Access-Control-Allow-Headers", strings.Join(cfg.AllowHeaders, ", "))
				if cfg.MaxAge > 0 {
					h.Set("Access-Control-Max-Age", strconv.Itoa(cfg.MaxAge))
				}
				c.W.WriteHeader(http.StatusNoContent)
				return nil
			}
			return next(c)
		}
	}
}
