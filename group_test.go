package aemeath_test

import (
	"net/http/httptest"
	"testing"

	"github.com/naufalraism/aemeath"
)

func TestGroupRouteAndMiddleware(t *testing.T) {
	app := aemeath.New()

	// Group middleware that adds an X-Admin header
	adminMW := func(next aemeath.HandlerFunc) aemeath.HandlerFunc {
		return func(c *aemeath.Ctx) error {
			c.W.Header().Set("X-Admin", "true")
			return next(c)
		}
	}

	admin := app.Group("/admin", adminMW)

	aemeath.GroupGET(admin, "/dashboard", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
		return map[string]string{"status": "ok"}, nil
	})

	// Request the route
	req := httptest.NewRequest("GET", "/admin/dashboard", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}

	// Group middleware must have run
	if rec.Header().Get("X-Admin") != "true" {
		t.Fatal("group middleware did not run")
	}
}
