package aemeath_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

func corsPreflight(mw aemeath.Middleware, origin string) *httptest.ResponseRecorder {
	app := aemeath.New()
	app.Use(mw)
	app.Handle("POST", "/x", func(c *aemeath.Ctx) error { return c.Text(200, "x") })
	r := httptest.NewRequest("OPTIONS", "/x", nil)
	r.Header.Set("Origin", origin)
	r.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}

func panicMessage(f func()) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg, _ = r.(string)
			if msg == "" {
				msg = "panic"
			}
		}
	}()
	f()
	return ""
}

// A CORS configuration that cannot do what it says must fail at startup,
// not silently allow nobody: a second config was ignored, and a config
// without origins allowed no origin.
func TestCORSRefusesConfigsThatAllowNobody(t *testing.T) {
	two := panicMessage(func() {
		aemeath.CORS(aemeath.CORSConfig{AllowOrigins: []string{"https://a.test"}}, aemeath.CORSConfig{AllowOrigins: []string{"https://b.test"}})
	})
	if !strings.Contains(two, "one") {
		t.Errorf("two configs: panic %q, want a message saying CORS takes one", two)
	}
	none := panicMessage(func() { aemeath.CORS(aemeath.CORSConfig{AllowCredentials: true}) })
	if !strings.Contains(none, "AllowOrigins") {
		t.Errorf("no origins: panic %q, want a message naming AllowOrigins", none)
	}
	if msg := panicMessage(func() { aemeath.CORS() }); msg != "" {
		t.Errorf("CORS() panicked: %q", msg)
	}
}

// MaxAge works like AllowMethods and AllowHeaders: left at zero it takes
// the default (a day), so browsers do not repeat the preflight before every
// request. A negative MaxAge sends no header.
func TestCORSMaxAgeDefaultsLikeTheOtherFields(t *testing.T) {
	cfg := aemeath.CORSConfig{AllowOrigins: []string{"https://a.test"}}
	if got := corsPreflight(aemeath.CORS(cfg), "https://a.test").Header().Get("Access-Control-Max-Age"); got != "86400" {
		t.Errorf("MaxAge 0: Access-Control-Max-Age = %q, want 86400", got)
	}
	cfg.MaxAge = 600
	if got := corsPreflight(aemeath.CORS(cfg), "https://a.test").Header().Get("Access-Control-Max-Age"); got != "600" {
		t.Errorf("MaxAge 600: Access-Control-Max-Age = %q", got)
	}
	cfg.MaxAge = -1
	if h := corsPreflight(aemeath.CORS(cfg), "https://a.test").Header(); h.Get("Access-Control-Max-Age") != "" || h.Get("Access-Control-Allow-Origin") != "https://a.test" {
		t.Errorf("MaxAge -1: headers %v, want no Max-Age and the origin allowed", h)
	}
}

// Gzip takes one minimum size; a second value was ignored silently.
func TestGzipTakesOneMinimumSize(t *testing.T) {
	if msg := panicMessage(func() { aemeath.Gzip(512, 2048) }); !strings.Contains(msg, "one") {
		t.Errorf("Gzip(512, 2048): panic %q, want a message saying it takes one size", msg)
	}
	for _, args := range [][]int{nil, {512}} {
		if msg := panicMessage(func() { aemeath.Gzip(args...) }); msg != "" {
			t.Errorf("Gzip(%v) panicked: %q", args, msg)
		}
	}
}
