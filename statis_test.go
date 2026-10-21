package aemeath_test

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/naufalraism/aemeath"
)

func TestStaticFile(t *testing.T) {
	// Buat folder sementara
	dir, _ := os.MkdirTemp("", "aemeath-static-*")
	defer func() { _ = os.RemoveAll(dir) }()

	// PERBAIKAN: Gunakan nama file selain index.html agar tidak memicu redirect 301 dari Go
	filePath := filepath.Join(dir, "hello.html")
	_ = os.WriteFile(filePath, []byte("<h1>Hello</h1>"), 0644)

	app := aemeath.New()
	app.Static("/public", dir)

	// Minta file tersebut
	req := httptest.NewRequest("GET", "/public/hello.html", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("gagal ambil static file, status: %d", rec.Code)
	}

	body := rec.Body.String()
	if body != "<h1>Hello</h1>" {
		t.Fatalf("isi file tidak cocok: %s", body)
	}
}
