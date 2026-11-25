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
	MaxAge           int // seconds browsers may cache a preflight; 0 = a day, negative = no header
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
// CORS() allows any origin without credentials (DefaultCORSConfig). Pass
// one CORSConfig to choose: empty AllowMethods and AllowHeaders, and a zero
// MaxAge, take the defaults; a negative MaxAge sends no Access-Control-Max-Age.
//
// A configuration that cannot work panics at startup: more than one config,
// an empty AllowOrigins (no origin would be allowed), and AllowCredentials
// with the "*" origin, which browsers reject and which is unsafe to emulate.
func CORS(configs ...CORSConfig) Middleware {
	cfg := DefaultCORSConfig()
	if len(configs) > 1 {
		panic("aemeath: CORS takes one CORSConfig, not several: put every origin in AllowOrigins")
	}
	if len(configs) == 1 {
		def := DefaultCORSConfig()
		cfg = configs[0]
		if len(cfg.AllowOrigins) == 0 {
			panic(`aemeath: CORSConfig.AllowOrigins is empty, so no origin would be allowed: list the origins (for example "https://app.example.com"), or call CORS() to allow any origin`)
		}
		if len(cfg.AllowMethods) == 0 {
			cfg.AllowMethods = def.AllowMethods
		}
		if len(cfg.AllowHeaders) == 0 {
			cfg.AllowHeaders = def.AllowHeaders
		}
		if cfg.MaxAge == 0 {
			cfg.MaxAge = def.MaxAge
		}
	}
	if cfg.AllowCredentials {
		for _, o := range cfg.AllowOrigins {
			if o == "*" {
				panic("aemeath: CORS: AllowCredentials cannot be combined with the \"*\" origin; list the allowed origins explicitly")
			}
		}
	}

	// The answer depends on the Origin header unless the only allowed origin
	// is "*", which every origin gets alike. Say so on every response, including those without CORS
	// headers: otherwise a shared cache can store one of those and serve it
	// to an allowed origin, whose browser then refuses it.
	varies := false
	for _, o := range cfg.AllowOrigins {
		if o != "*" {
			varies = true
		}
	}

	return func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) error {
			if varies {
				c.W.Header().Add("Vary", "Origin")
			}
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
