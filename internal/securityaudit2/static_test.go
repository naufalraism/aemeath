package securityaudit2

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/storage"
)

// storage.Local neutralizes .html, .svg, .js ... but a name with no extension
// or an unknown one kept it, and the static file server guessed the type from
// the content: an uploaded page ran as HTML on the site's own origin.
func TestStatic_UploadWithoutKnownExtensionIsNotServedAsHTML(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewLocal(dir)
	app := aemeath.New()
	app.Static("/uploads", dir)
	page := "<!doctype html><script>alert(document.cookie)</script>"
	for _, name := range []string{"note", "report.dat", "x.unknownext"} {
		stored, err := store.Save(t.Context(), name, strings.NewReader(page))
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("GET", "/uploads/"+stored, nil))
		ct, nosniff := rec.Header().Get("Content-Type"), rec.Header().Get("X-Content-Type-Options")
		t.Logf("upload %-14q stored as %-40q -> %d, Content-Type %q, X-Content-Type-Options %q", name, stored, rec.Code, ct, nosniff)
		if rec.Code != 200 || rec.Body.String() != page {
			t.Errorf("%s: the file itself must still be served (status %d)", name, rec.Code)
		}
		if strings.HasPrefix(ct, "text/html") || nosniff != "nosniff" {
			t.Errorf("%s is served as %q without nosniff: stored XSS on the site's own origin", name, ct)
		}
	}
}

func TestStatic_NosniffByDefault(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := aemeath.New()
	app.Static("/s", dir)
	for file, want := range map[string]string{"a.txt": "text/plain", "app.js": "javascript"} {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("GET", "/s/"+file, nil))
		t.Logf("%s without SecurityHeaders: Content-Type %q, X-Content-Type-Options %q", file, rec.Header().Get("Content-Type"), rec.Header().Get("X-Content-Type-Options"))
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s is served without X-Content-Type-Options: nosniff", file)
		}
		if !strings.Contains(rec.Header().Get("Content-Type"), want) {
			t.Errorf("%s: known types must keep their Content-Type", file)
		}
	}
}

func TestStatic_FileHelperAndIndexPagesStillWork(t *testing.T) {
	dir := t.TempDir()
	page := "<!doctype html><script>alert(1)</script>"
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o750); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"upload": page, "docs/index.html": "<h1>docs</h1>", "logo.png": "\x89PNG\r\n\x1a\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	app := aemeath.New()
	app.Static("/s", dir)
	app.Handle("GET", "/download", func(c *aemeath.Ctx) error { return c.File(filepath.Join(dir, "upload")) })
	app.Handle("GET", "/logo", func(c *aemeath.Ctx) error { return c.File(filepath.Join(dir, "logo.png")) })

	for _, tc := range []struct {
		path, wantType string
		wantCode       int
	}{
		{"/download", "application/octet-stream", 200}, // c.File: same rule as Static
		{"/logo", "image/png", 200},
		{"/s/docs/", "text/html", 200}, // a directory's index page is still a page
		{"/s/docs", "", 301},           // and the missing slash still redirects
		{"/s/logo.png", "image/png", 200},
	} {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("GET", tc.path, nil))
		t.Logf("%-14s -> %d %q nosniff=%q", tc.path, rec.Code, rec.Header().Get("Content-Type"), rec.Header().Get("X-Content-Type-Options"))
		if rec.Code != tc.wantCode || !strings.HasPrefix(rec.Header().Get("Content-Type"), tc.wantType) {
			t.Errorf("%s: %d %q, want %d %q", tc.path, rec.Code, rec.Header().Get("Content-Type"), tc.wantCode, tc.wantType)
		}
		if tc.wantCode == 200 && rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s without nosniff", tc.path)
		}
	}
}
