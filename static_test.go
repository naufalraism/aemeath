package aemeath_test

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

func TestStaticFile(t *testing.T) {
	// Create a temporary directory
	dir, _ := os.MkdirTemp("", "aemeath-static-*")
	defer func() { _ = os.RemoveAll(dir) }()

	// Use a file name other than index.html so Go does not issue a 301 redirect.
	filePath := filepath.Join(dir, "hello.html")
	_ = os.WriteFile(filePath, []byte("<h1>Hello</h1>"), 0644)

	app := aemeath.New()
	app.Static("/public", dir)

	// Request the file
	req := httptest.NewRequest("GET", "/public/hello.html", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("failed to fetch static file, status: %d", rec.Code)
	}

	body := rec.Body.String()
	if body != "<h1>Hello</h1>" {
		t.Fatalf("file content mismatch: %s", body)
	}
}

// Dotfiles used to be served: app.Static("/", ".") leaked .env and .git.
func TestStaticHidesDotfiles(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(body), 0o644)
	}
	write(".env", "SECRET=1")
	write(".git/config", "[remote]")
	write("assets/.htpasswd", "admin:x")
	write(".well-known/security.txt", "Contact: me")
	write("app.js", "ok")

	for _, prefix := range []string{"/", "/public"} {
		app := aemeath.New()
		app.Static(prefix, dir)
		base := strings.TrimSuffix(prefix, "/")
		for path, want := range map[string]int{
			"/.env":                     404,
			"/.git/config":              404,
			"/assets/.htpasswd":         404,
			"/%2eenv":                   404,
			"/.well-known/security.txt": 200,
			"/app.js":                   200,
		} {
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, httptest.NewRequest("GET", base+path, nil))
			if rec.Code != want {
				t.Errorf("%s%s: status %d, want %d (body %q)", base, path, rec.Code, want, rec.Body.String())
			}
			if want == 404 && strings.Contains(rec.Body.String(), "SECRET") {
				t.Errorf("%s%s leaked the file", base, path)
			}
		}
	}
}
