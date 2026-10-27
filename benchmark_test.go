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

	b.ReportAllocs()

	// b.Loop (Go 1.24+) handles timer resets and keeps the compiler from
	// optimizing the body away. A fresh recorder per iteration keeps the
	// response buffer from growing across iterations.
	for b.Loop() {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}
