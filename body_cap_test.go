package aemeath_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

// Every request body is capped, also for handlers that read c.R.Body
// themselves; MaxBodySize replaces the cap (it does not nest inside it).
func TestDefaultRequestBodyCap(t *testing.T) {
	old := aemeath.DefaultMaxRequestBytes
	aemeath.DefaultMaxRequestBytes = 1000
	defer func() { aemeath.DefaultMaxRequestBytes = old }()

	app := aemeath.New()
	read := func(c *aemeath.Ctx) error {
		n, err := io.Copy(io.Discard, c.R.Body)
		if err != nil {
			return err
		}
		return c.Text(200, fmt.Sprint(n))
	}
	app.Handle("POST", "/raw", read)
	app.Group("/up", aemeath.MaxBodySize(5000)).Handle("POST", "/x", read)
	app.Group("/free", aemeath.MaxBodySize(-1)).Handle("POST", "/x", read)
	app.Handle("GET", "/nobody", func(c *aemeath.Ctx) error {
		return c.Text(200, fmt.Sprint(c.R.Body == http.NoBody))
	})

	for _, tc := range []struct {
		path string
		size int
		want int
		body string
	}{
		{"/raw", 1000, 200, "1000"},
		{"/raw", 1001, 413, ""},
		{"/up/x", 4000, 200, "4000"},   // raised above the default cap
		{"/up/x", 5001, 413, ""},       // its own limit applies
		{"/free/x", 9000, 200, "9000"}, // no cap
	} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("POST", tc.path, strings.NewReader(strings.Repeat("a", tc.size))))
		if w.Code != tc.want || (tc.body != "" && w.Body.String() != tc.body) {
			t.Errorf("POST %s with %d bytes: %d %q, want %d %q", tc.path, tc.size, w.Code, w.Body.String(), tc.want, tc.body)
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/nobody", nil)
	r.Body = http.NoBody
	app.ServeHTTP(w, r)
	if w.Body.String() != "true" {
		t.Errorf("a request without a body got a wrapped body: %q", w.Body.String())
	}
}
