package aemeath_test

import (
	"net/http/httptest"
	"os"
	"path/filepath"
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
