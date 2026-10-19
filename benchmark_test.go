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

	// ReportAllocs melihat seberapa banyak memori yang dialokasikan
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		app.ServeHTTP(rec, req)
	}
}
