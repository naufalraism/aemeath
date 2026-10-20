package aemeath_test

import (
	"net/http/httptest"
	"testing"

	"github.com/naufalraism/aemeath"
)

func BenchmarkHelloRoute(b *testing.B) {
	app := aemeath.New()
	aemeath.GET(app, "/hello", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
		return map[string]string{"message": "hi"}, nil
	})

	req := httptest.NewRequest("GET", "/hello", nil)
	rec := httptest.NewRecorder()

	b.ReportAllocs()

	// Menggunakan fitur modern Go 1.24+: b.Loop()
	// Ini otomatis menangani b.ResetTimer() dan mencegah kompilator menghapus kodemu.
	for b.Loop() {
		app.ServeHTTP(rec, req)
	}
}
