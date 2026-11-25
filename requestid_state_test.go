package aemeath_test

import (
	"net/http/httptest"
	"testing"

	"github.com/naufalraism/aemeath"
)

// c.RequestID() reports the id the RequestID middleware chose (and sent in
// X-Request-Id), whatever application code stores with c.Set.
func TestRequestIDCannotBeOverwrittenThroughState(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.RequestID(), func(next aemeath.HandlerFunc) aemeath.HandlerFunc {
		return func(c *aemeath.Ctx) error {
			c.Set("request_id", "spoofed")
			return next(c)
		}
	})
	app.Handle("GET", "/", func(c *aemeath.Ctx) error { return c.Text(200, c.RequestID()) })
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Request-Id", "abc-123")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if got := w.Body.String(); got != "abc-123" || w.Header().Get("X-Request-Id") != "abc-123" {
		t.Errorf("RequestID() = %q, header %q; want abc-123 for both", got, w.Header().Get("X-Request-Id"))
	}
}
