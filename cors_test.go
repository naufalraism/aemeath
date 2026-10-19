package aemeath_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/naufalraism/aemeath"
)

func TestCORSMiddleware(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.CORS())

	app.Handle("GET", "/test", func(c *aemeath.Ctx) error {
		return c.JSON(200, map[string]string{"ok": "yes"})
	})

	// 1. Tes Preflight (OPTIONS)
	req := httptest.NewRequest("OPTIONS", "/test", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("preflight allow-origin = %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}

	// 2. Tes Request Biasa dengan Origin
	req = httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("request allow-origin = %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}
