package aemeath_test

import (
	"net/http/httptest"
	"testing"

	"github.com/naufalraism/aemeath"
)

// RequireAuth used to only document the route: without an authentication
// middleware the route was open, although its name says otherwise.
func TestRequireAuthRefusesUnauthenticatedRequests(t *testing.T) {
	type out struct {
		OK bool `json:"ok"`
	}
	h := func(c *aemeath.Ctx, _ struct{}) (out, error) { return out{true}, nil }
	get := func(app *aemeath.App, path string) int {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w.Code
	}

	app := aemeath.New()
	aemeath.GET(app, "/open", h)
	aemeath.GET(app, "/secret", h, aemeath.RequireAuth())
	g := app.Group("/admin")
	g.Options(aemeath.RequireAuth())
	aemeath.GET(g, "/users", h)
	for path, want := range map[string]int{"/open": 200, "/secret": 401, "/admin/users": 401} {
		if got := get(app, path); got != want {
			t.Errorf("no auth middleware, %s: %d, want %d", path, got, want)
		}
	}

	authed := aemeath.New()
	authed.Use(func(next aemeath.HandlerFunc) aemeath.HandlerFunc {
		return func(c *aemeath.Ctx) error {
			if c.R.Header.Get("X-User") != "" {
				c.MarkAuthenticated()
			}
			return next(c)
		}
	})
	aemeath.GET(authed, "/secret", h, aemeath.RequireAuth())
	if got := get(authed, "/secret"); got != 401 {
		t.Errorf("middleware did not mark the request: %d, want 401", got)
	}
	r := httptest.NewRequest("GET", "/secret", nil)
	r.Header.Set("X-User", "ann")
	w := httptest.NewRecorder()
	authed.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("marked request: %d, want 200", w.Code)
	}
}
