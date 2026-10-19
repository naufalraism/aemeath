package aemeath_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

type helloIn struct {
	Name string `path:"name"`
	Lang string `query:"lang"`
}
type helloOut struct {
	Message string `json:"message"`
}
type userIn struct {
	Name string `json:"name" validate:"required"`
}
type userOut struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func newApp() *aemeath.App {
	app := aemeath.New()
	app.Use(aemeath.Recover())
	aemeath.GET(app, "/hello/{name}", func(c *aemeath.Ctx, in helloIn) (helloOut, error) {
		return helloOut{Message: in.Lang + ":" + in.Name}, nil
	}, aemeath.Summary("Sapa"))
	aemeath.POST(app, "/users", func(c *aemeath.Ctx, in userIn) (userOut, error) {
		return userOut{ID: 1, Name: in.Name}, nil
	}, aemeath.Status(201))
	aemeath.GET(app, "/boom", func(c *aemeath.Ctx, _ struct{}) (struct{}, error) {
		panic("duar")
	})
	app.EnableDocs(aemeath.Info{Title: "Test"})
	return app
}

func do(h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestBindsPathAndQuery(t *testing.T) {
	rec := do(newApp(), "GET", "/hello/Aemeath?lang=id", "")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var out helloOut
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Message != "id:Aemeath" {
		t.Fatalf("message = %q", out.Message)
	}
}

func TestPostValidAndStatus(t *testing.T) {
	rec := do(newApp(), "POST", "/users", `{"name":"Aemeath"}`)
	if rec.Code != 201 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
}

func TestValidationAndBadJSON(t *testing.T) {
	app := newApp()
	if rec := do(app, "POST", "/users", `{}`); rec.Code != 400 {
		t.Fatalf("required: status = %d", rec.Code)
	}
	if rec := do(app, "POST", "/users", `{bad`); rec.Code != 400 {
		t.Fatalf("bad json: status = %d", rec.Code)
	}
}

func TestRecover(t *testing.T) {
	if rec := do(newApp(), "GET", "/boom", ""); rec.Code != 500 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestUseAfterRoute(t *testing.T) {
	app := aemeath.New()
	aemeath.GET(app, "/ping", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
		return map[string]string{"ok": "yes"}, nil
	})
	app.Use(func(next aemeath.HandlerFunc) aemeath.HandlerFunc {
		return func(c *aemeath.Ctx) error {
			c.W.Header().Set("X-Test", "1")
			return next(c)
		}
	})
	if rec := do(app, "GET", "/ping", ""); rec.Header().Get("X-Test") != "1" {
		t.Fatal("middleware tidak jalan")
	}
}

func TestOpenAPI(t *testing.T) {
	rec := do(newApp(), "GET", "/openapi.json", "")
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	paths := doc["paths"].(map[string]any)
	if _, ok := paths["/hello/{name}"].(map[string]any)["get"]; !ok {
		t.Fatal("GET /hello/{name} tidak ada di spec")
	}
	post := paths["/users"].(map[string]any)["post"].(map[string]any)
	if post["requestBody"] == nil {
		t.Fatal("requestBody POST /users tidak ada")
	}
	if do(newApp(), "GET", "/docs", "").Code != 200 {
		t.Fatal("/docs tidak jalan")
	}
}
