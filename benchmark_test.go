package aemeath_test

import (
	"net/http/httptest"
	"strings"
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

// BenchmarkValidatedJSONPost is the second row of the README's allocation
// table: a JSON body with two validated fields.
func BenchmarkValidatedJSONPost(b *testing.B) {
	type in struct {
		Name  string `json:"name"  validate:"required,min=3"`
		Email string `json:"email" validate:"required,email"`
	}
	app := aemeath.New()
	aemeath.POST(app, "/users", func(c *aemeath.Ctx, v in) (in, error) { return v, nil })

	body := `{"name":"Ana","email":"ana@example.com"}`

	b.ReportAllocs()
	for b.Loop() {
		req := httptest.NewRequest("POST", "/users", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		app.ServeHTTP(httptest.NewRecorder(), req)
	}
}
