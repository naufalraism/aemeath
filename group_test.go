package aemeath_test

import (
	"net/http/httptest"
	"testing"

	"github.com/naufalraism/aemeath"
)

func TestGroupRouteAndMiddleware(t *testing.T) {
	app := aemeath.New()

	// Middleware grup yang menambahkan header X-Admin
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

	// Tes rute
	req := httptest.NewRequest("GET", "/admin/dashboard", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}

	// Tes middleware grup berjalan
	if rec.Header().Get("X-Admin") != "true" {
		t.Fatal("middleware group tidak jalan")
	}
}
