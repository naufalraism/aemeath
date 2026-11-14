package aemeath_test

import (
	"errors"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

// A value that cannot be encoded used to produce 200 with an empty body,
// because the status was sent before encoding.
func TestUnencodableOutputIs500(t *testing.T) {
	app := aemeath.New()
	aemeath.GET(app, "/nan", func(c *aemeath.Ctx, _ struct{}) (map[string]float64, error) {
		return map[string]float64{"x": math.NaN()}, nil
	})
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/nan", nil))
	if w.Code != 500 || !strings.Contains(w.Body.String(), "internal server error") {
		t.Fatalf("got %d %q, want a 500 JSON error", w.Code, w.Body.String())
	}
}

// An error returned after the handler started writing used to be appended to
// the body: "partial-{"error":...}".
func TestErrorAfterResponseStartedIsNotAppended(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.Logger(), aemeath.Recover())
	aemeath.GET(app, "/h", func(c *aemeath.Ctx, _ struct{}) (aemeath.Handled, error) {
		_ = c.Text(200, "partial-")
		return aemeath.Handled{}, errors.New("boom")
	})
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/h", nil))
	if got := w.Body.String(); got != "partial-" {
		t.Fatalf("body = %q, want only what the handler wrote", got)
	}
}

// An error before anything was written is still answered normally.
func TestErrorBeforeWriteStillAnswered(t *testing.T) {
	app := aemeath.New()
	aemeath.GET(app, "/e", func(c *aemeath.Ctx, _ struct{}) (aemeath.Handled, error) {
		c.W.Header().Set("X-Partial", "1") // headers alone do not start the response
		return aemeath.Handled{}, aemeath.NotFound("nope")
	})
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/e", nil))
	if w.Code != 404 || !strings.Contains(w.Body.String(), "nope") {
		t.Fatalf("got %d %q", w.Code, w.Body.String())
	}
}

// c.File still works through the tracking writer (ReadFrom path).
func TestFileThroughTracker(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.txt")
	_ = os.WriteFile(p, []byte(strings.Repeat("x", 100_000)), 0o644)
	app := aemeath.New()
	aemeath.GET(app, "/f", func(c *aemeath.Ctx, _ struct{}) (aemeath.Handled, error) {
		return aemeath.Handled{}, c.File(p)
	})
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/f", nil))
	if w.Code != 200 || w.Body.Len() != 100_000 {
		t.Fatalf("got %d, %d bytes", w.Code, w.Body.Len())
	}
}
