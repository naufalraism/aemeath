package benchmarks

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofiber/fiber/v2"
	"github.com/naufalraism/aemeath"
)

// ------------------------------------------------------
// 1. Benchmark: Go Standard Library (net/http)
// ------------------------------------------------------
func BenchmarkNetHTTP(b *testing.B) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"message":"Hello Aemeath"}`))
	})

	req := httptest.NewRequest("GET", "/hello", nil)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
	}
}

// ------------------------------------------------------
// 2. Benchmark: Gin
// ------------------------------------------------------
func BenchmarkGin(b *testing.B) {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.GET("/hello", func(c *gin.Context) {
		c.JSON(200, gin.H{"message": "Hello Aemeath"})
	})

	req := httptest.NewRequest("GET", "/hello", nil)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
	}
}

// ------------------------------------------------------
// 3. Benchmark: Fiber
//
// NOTE: app.Test sends the request through an in-memory connection, so this
// number is NOT comparable with the others, which call ServeHTTP directly.
// ------------------------------------------------------
func BenchmarkFiber(b *testing.B) {
	app := fiber.New()
	app.Get("/hello", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"message": "Hello Aemeath"})
	})

	req := httptest.NewRequest("GET", "/hello", nil)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = app.Test(req)
	}
}

// ------------------------------------------------------
// 4. Benchmark: Aemeath
// ------------------------------------------------------
func BenchmarkAemeath(b *testing.B) {
	app := aemeath.New()

	type HelloOut struct {
		Message string `json:"message"`
	}

	aemeath.GET(app, "/hello", func(c *aemeath.Ctx, _ struct{}) (HelloOut, error) {
		return HelloOut{Message: "Hello Aemeath"}, nil
	})

	req := httptest.NewRequest("GET", "/hello", nil)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
	}
}
