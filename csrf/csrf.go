// Package csrf protects cookie-authenticated routes against cross-site request
// forgery. It needs the session middleware (the token lives in the session).
//
// Unsafe methods (anything but GET/HEAD/OPTIONS) must carry the session's token
// in the X-CSRF-Token header, and any Origin header present must be same-origin
// or trusted.
package csrf

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/url"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/session"
)

// Config configures Middleware.
type Config struct {
	Header         string   // default "X-CSRF-Token"
	Cookie         string   // if set, also publish the token in a readable cookie (e.g. "XSRF-TOKEN")
	TrustedOrigins []string // extra allowed origins, e.g. "https://app.example.com"
	Insecure       bool     // allow the optional cookie over plain HTTP (development only)
}

const sessionKey = "aemeath.csrf"

// Token returns the request's CSRF token, creating it in the session if needed.
func Token(c *aemeath.Ctx) string {
	s := session.From(c)
	if t := s.String(sessionKey); t != "" {
		return t
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("csrf: crypto/rand failed: " + err.Error())
	}
	t := base64.RawURLEncoding.EncodeToString(b)
	s.Set(sessionKey, t)
	return t
}

func safe(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// Middleware enforces CSRF protection. Install it after session.Middleware.
func Middleware(cfg Config) aemeath.Middleware {
	if cfg.Header == "" {
		cfg.Header = "X-CSRF-Token"
	}
	trusted := map[string]bool{}
	for _, o := range cfg.TrustedOrigins {
		trusted[o] = true
	}
	return func(next aemeath.HandlerFunc) aemeath.HandlerFunc {
		return func(c *aemeath.Ctx) error {
			tok := Token(c)
			if cfg.Cookie != "" {
				// #nosec G124 -- deliberately not HttpOnly: front-end code must read this token to send it back in the header.
				c.SetCookie(&http.Cookie{Name: cfg.Cookie, Value: tok, Path: "/", Secure: !cfg.Insecure, SameSite: http.SameSiteLaxMode})
			}
			if safe(c.R.Method) {
				return next(c)
			}
			if o := c.R.Header.Get("Origin"); o != "" && !sameOrigin(o, c.R) && !trusted[o] {
				return aemeath.Forbidden("csrf: cross-origin request rejected")
			}
			got := c.R.Header.Get(cfg.Header)
			if subtle.ConstantTimeCompare([]byte(got), []byte(tok)) != 1 {
				return aemeath.Forbidden("csrf: missing or invalid token")
			}
			return next(c)
		}
	}
}

func sameOrigin(origin string, r *http.Request) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}
