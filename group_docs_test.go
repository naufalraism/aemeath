package aemeath_test

import (
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

// adminOnly answers 401 unless the request carries X-Admin: yes.
func adminOnly(next aemeath.HandlerFunc) aemeath.HandlerFunc {
	return func(c *aemeath.Ctx) error {
		if c.R.Header.Get("X-Admin") != "yes" {
			return aemeath.Unauthorized("admins only")
		}
		return next(c)
	}
}

func adminApp() (*aemeath.App, *aemeath.Group) {
	app := aemeath.New()
	aemeath.GET(app, "/ping", func(*aemeath.Ctx, struct{}) (map[string]string, error) { return nil, nil })
	admin := app.Group("/admin", adminOnly)
	return app, admin
}

func TestGroupEnableDocsSitsBehindTheGroup(t *testing.T) {
	app, admin := adminApp()
	admin.EnableDocs(aemeath.Info{Title: "Internal", Version: "1"})

	for _, path := range []string{"/admin/docs", "/admin/openapi.json"} {
		if rec := get(app, path); rec.Code != 401 {
			t.Errorf("%s without the header: status %d, want 401 (the group's middleware)", path, rec.Code)
		}
		if rec := get(app, path, "X-Admin", "yes"); rec.Code != 200 {
			t.Errorf("%s as admin: status %d, want 200", path, rec.Code)
		}
	}
	// The document still describes the whole app, not only the group.
	if body := get(app, "/admin/openapi.json", "X-Admin", "yes").Body.String(); !strings.Contains(body, `"/ping"`) {
		t.Errorf("the group's openapi.json does not list /ping:\n%s", body)
	}
	// The page finds the document of its own group.
	rec := get(app, "/admin/docs", "X-Admin", "yes")
	if !strings.Contains(rec.Body.String(), `data-url="openapi.json"`) {
		t.Errorf("the docs page does not load openapi.json next to itself:\n%s", rec.Body.String())
	}
	// Nothing is left public at the root.
	for _, path := range []string{"/docs", "/openapi.json"} {
		if rec := get(app, path); rec.Code != 404 {
			t.Errorf("%s: status %d, want 404", path, rec.Code)
		}
	}
}

func TestGroupEnableClientSitsBehindTheGroup(t *testing.T) {
	app, admin := adminApp()
	admin.EnableClient()

	for path, want := range map[string]string{
		"/admin/client.ts":  "getPing",
		"/admin/schemas.ts": `import { z } from "zod"`,
		"/admin/hooks.ts":   "useGetPing",
	} {
		if rec := get(app, path); rec.Code != 401 {
			t.Errorf("%s without the header: status %d, want 401", path, rec.Code)
		}
		rec := get(app, path, "X-Admin", "yes")
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s as admin: status %d, body without %q", path, rec.Code, want)
		}
		if rec := get(app, strings.TrimPrefix(path, "/admin")); rec.Code != 404 {
			t.Errorf("%s: status %d, want 404", strings.TrimPrefix(path, "/admin"), rec.Code)
		}
	}
	// A route registered after the first request still shows up.
	aemeath.GET(app, "/late", func(*aemeath.Ctx, struct{}) (map[string]string, error) { return nil, nil })
	if !strings.Contains(get(app, "/admin/client.ts", "X-Admin", "yes").Body.String(), "getLate") {
		t.Error("the group's client.ts was not regenerated after a new route was registered")
	}
}
