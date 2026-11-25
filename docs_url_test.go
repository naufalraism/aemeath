package aemeath_test

import (
	"net/http"
	"net/url"
	"regexp"
	"testing"

	"github.com/naufalraism/aemeath"
)

// specURL returns the address the docs page served at page loads its OpenAPI
// document from, resolved the way a browser resolves it.
func specURL(t *testing.T, h http.Handler, page string) string {
	t.Helper()
	rec := get(h, page)
	if rec.Code != 200 {
		t.Fatalf("GET %s: status %d", page, rec.Code)
	}
	m := regexp.MustCompile(`id="api-reference" data-url="([^"]*)"`).FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatalf("GET %s: no data-url in the page", page)
	}
	ref, err := url.Parse(m[1])
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse("https://example.com" + page)
	return base.ResolveReference(ref).Path
}

// Behind a reverse proxy that mounts the app under a prefix (/api/docs on
// the outside, /docs inside), an absolute "/openapi.json" made the browser
// ask for https://host/openapi.json, outside the app. A relative URL keeps
// the request next to the page.
func TestDocsPageLoadsTheSpecNextToItself(t *testing.T) {
	app := aemeath.New()
	app.EnableDocs(aemeath.Info{Title: "t", Version: "1"})
	proxied := http.StripPrefix("/api", app)

	if got := specURL(t, app, "/docs"); got != "/openapi.json" {
		t.Errorf("at /docs the spec loads from %s, want /openapi.json", got)
	}
	got := specURL(t, proxied, "/api/docs")
	if got != "/api/openapi.json" {
		t.Errorf("behind a /api prefix the spec loads from %s, want /api/openapi.json", got)
	}
	if rec := get(proxied, got); rec.Code != 200 {
		t.Errorf("GET %s through the proxy: status %d", got, rec.Code)
	}
}
