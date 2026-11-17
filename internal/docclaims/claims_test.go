// Package docclaims checks statements made in README.md and docs/ against the
// running framework. Each subtest quotes the claim it proves.
package docclaims

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/aetest"
	"github.com/naufalraism/aemeath/csrf"
	"github.com/naufalraism/aemeath/jobs"
	"github.com/naufalraism/aemeath/metrics"
	"github.com/naufalraism/aemeath/session"
)

func do(h http.Handler, method, target, body string, hdr ...string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func panics(f func()) (msg any) {
	defer func() { msg = recover() }()
	f()
	return nil
}

type helloIn struct {
	Name string `path:"name"`
	Lang string `query:"lang" validate:"oneof=en es"`
}
type helloOut struct {
	Message string `json:"message"`
}

func TestQuickStart(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.Logger(), aemeath.Recover(), aemeath.CORS())
	aemeath.GET(app, "/hello/{name}", func(_ *aemeath.Ctx, in helloIn) (helloOut, error) {
		g := "Hello"
		if in.Lang == "es" {
			g = "Hola"
		}
		return helloOut{Message: g + ", " + in.Name}, nil
	})
	if got := strings.TrimSpace(do(app, "GET", "/hello/Aemeath?lang=es", "").Body.String()); got != `{"message":"Hola, Aemeath"}` {
		t.Errorf("README quick start, first curl: %s", got)
	}
	rec := do(app, "GET", "/hello/Aemeath?lang=fr", "")
	want := `{"error":"validation failed","errors":[{"field":"lang","rule":"oneof","message":"must be one of: en, es"}]}`
	if rec.Code != 422 || strings.TrimSpace(rec.Body.String()) != want {
		t.Errorf("README quick start, second curl: %d %s", rec.Code, rec.Body.String())
	}
}

type mixIn struct {
	ID   int    `path:"id"`
	Name string `json:"name"`
	Page int    `query:"page"`
}

func TestRoutingAndBinding(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.RequestID())
	aemeath.POST(app, "/items/{id}", func(_ *aemeath.Ctx, in mixIn) (mixIn, error) { return in, nil })
	aemeath.DELETE(app, "/items/{id}", func(_ *aemeath.Ctx, _ struct {
		ID int `path:"id"`
	}) (struct{}, error) {
		return struct{}{}, nil
	}, aemeath.Status(204))
	aemeath.GET(app, "/boom", func(_ *aemeath.Ctx, _ struct{}) (struct{}, error) {
		return struct{}{}, errors.New("secret database detail")
	})
	aemeath.GET(app, "/nf", func(_ *aemeath.Ctx, _ struct{}) (struct{}, error) {
		return struct{}{}, aemeath.NotFound("user not found")
	})
	g := app.Group("/admin")
	aemeath.GroupGET(g, "/", func(_ *aemeath.Ctx, _ struct{}) (helloOut, error) { return helloOut{"root"}, nil })

	t.Run("Unknown paths return 404; wrong method 405 with Allow; both pass through global middlewares", func(t *testing.T) {
		r := do(app, "GET", "/nope", "")
		if r.Code != 404 || r.Header().Get("X-Request-Id") == "" {
			t.Errorf("404: %d, X-Request-Id %q", r.Code, r.Header().Get("X-Request-Id"))
		}
		r = do(app, "PUT", "/boom", "")
		if r.Code != 405 || r.Header().Get("Allow") == "" || r.Header().Get("X-Request-Id") == "" {
			t.Errorf("405: %d, Allow %q, X-Request-Id %q", r.Code, r.Header().Get("Allow"), r.Header().Get("X-Request-Id"))
		}
	})
	t.Run("204 sends no body", func(t *testing.T) {
		if r := do(app, "DELETE", "/items/1", ""); r.Code != 204 || r.Body.Len() != 0 {
			t.Errorf("%d %q", r.Code, r.Body.String())
		}
	})
	t.Run(`errors are {"error": "message"}; any other error becomes a generic 500`, func(t *testing.T) {
		if r := do(app, "GET", "/nf", ""); r.Code != 404 || strings.TrimSpace(r.Body.String()) != `{"error":"user not found"}` {
			t.Errorf("%d %s", r.Code, r.Body.String())
		}
		if r := do(app, "GET", "/boom", ""); r.Code != 500 || strings.Contains(r.Body.String(), "secret") {
			t.Errorf("%d %s", r.Code, r.Body.String())
		}
	})
	t.Run("Malformed input is 400; a body over 1 MiB is 413; a non-JSON Content-Type is 415", func(t *testing.T) {
		if r := do(app, "POST", "/items/1", `{"name":`); r.Code != 400 {
			t.Errorf("invalid JSON: %d", r.Code)
		}
		if r := do(app, "POST", "/items/1?page=abc", `{}`); r.Code != 400 {
			t.Errorf("?page=abc: %d", r.Code)
		}
		if r := do(app, "POST", "/items/1", `{"name":"`+strings.Repeat("x", 1<<20)+`"}`); r.Code != 413 {
			t.Errorf("large body: %d", r.Code)
		}
		if r := do(app, "POST", "/items/1", `{}`, "Content-Type", "text/plain"); r.Code != 415 {
			t.Errorf("text/plain: %d", r.Code)
		}
	})
	t.Run(`A group's "/" route matches the prefix exactly, not everything below it`, func(t *testing.T) {
		if r := do(app, "GET", "/admin", ""); r.Code != 200 {
			t.Errorf("/admin: %d", r.Code)
		}
		if r := do(app, "GET", "/admin/other", ""); r.Code != 404 {
			t.Errorf("/admin/other: %d", r.Code)
		}
	})
	t.Run("Path/query/header/cookie values override anything with the same name in the body", func(t *testing.T) {
		type in struct {
			ID int `path:"id" json:"id"`
		}
		a := aemeath.New()
		aemeath.POST(a, "/x/{id}", func(_ *aemeath.Ctx, v in) (in, error) { return v, nil })
		if r := do(a, "POST", "/x/7", `{"id":99}`); !strings.Contains(r.Body.String(), `"id":7`) {
			t.Errorf("%d %s", r.Code, r.Body.String())
		}
	})
}

func TestStartupPanics(t *testing.T) {
	cases := map[string]func(a *aemeath.App){
		"required scalar without a pointer": func(a *aemeath.App) {
			aemeath.POST(a, "/a", func(_ *aemeath.Ctx, _ struct {
				N int `json:"n" validate:"required"`
			}) (struct{}, error) {
				return struct{}{}, nil
			})
		},
		"required + omitempty": func(a *aemeath.App) {
			aemeath.POST(a, "/a", func(_ *aemeath.Ctx, _ struct {
				N string `json:"n,omitempty" validate:"required"`
			}) (struct{}, error) {
				return struct{}{}, nil
			})
		},
		"min on a bool": func(a *aemeath.App) {
			aemeath.POST(a, "/a", func(_ *aemeath.Ctx, _ struct {
				N bool `json:"n" validate:"min=1"`
			}) (struct{}, error) {
				return struct{}{}, nil
			})
		},
		"email on a number": func(a *aemeath.App) {
			aemeath.POST(a, "/a", func(_ *aemeath.Ctx, _ struct {
				N int `json:"n" validate:"email"`
			}) (struct{}, error) {
				return struct{}{}, nil
			})
		},
		"a bad regex": func(a *aemeath.App) {
			aemeath.POST(a, "/a", func(_ *aemeath.Ctx, _ struct {
				N string `json:"n" validate:"pattern=("`
			}) (struct{}, error) {
				return struct{}{}, nil
			})
		},
		"CORS credentials with *": func(a *aemeath.App) {
			a.Use(aemeath.CORS(aemeath.CORSConfig{AllowOrigins: []string{"*"}, AllowCredentials: true}))
		},
		"a form field on a GET route": func(a *aemeath.App) {
			aemeath.GET(a, "/a", func(_ *aemeath.Ctx, _ struct {
				N string `form:"n"`
			}) (struct{}, error) {
				return struct{}{}, nil
			})
		},
	}
	for name, f := range cases {
		t.Run("panics at startup: "+name, func(t *testing.T) {
			if panics(func() { f(aemeath.New()) }) == nil {
				t.Error("no panic")
			}
		})
	}
}

func TestMiddlewareTable(t *testing.T) {
	ok := func(_ *aemeath.Ctx, _ struct{}) (helloOut, error) {
		return helloOut{strings.Repeat("hello ", 400)}, nil
	}
	t.Run("Timeout(d): 504 when a handler that honors the context runs out", func(t *testing.T) {
		a := aemeath.New()
		a.Use(aemeath.Timeout(20 * time.Millisecond))
		aemeath.GET(a, "/", func(c *aemeath.Ctx, _ struct{}) (struct{}, error) {
			<-c.Context().Done()
			return struct{}{}, c.Context().Err()
		})
		if r := do(a, "GET", "/", ""); r.Code != 504 {
			t.Errorf("%d", r.Code)
		}
	})
	t.Run("MaxBodySize(n): typed routes answer 413", func(t *testing.T) {
		a := aemeath.New()
		a.Use(aemeath.MaxBodySize(10))
		aemeath.POST(a, "/", func(_ *aemeath.Ctx, in mixIn) (mixIn, error) { return in, nil })
		if r := do(a, "POST", "/", `{"name":"0123456789abcdef"}`); r.Code != 413 {
			t.Errorf("%d", r.Code)
		}
	})
	t.Run("RateLimiter(n, window): sends Retry-After on 429", func(t *testing.T) {
		a := aemeath.New()
		a.Use(aemeath.RateLimiter(1, time.Minute))
		aemeath.GET(a, "/", ok)
		do(a, "GET", "/", "")
		if r := do(a, "GET", "/", ""); r.Code != 429 || r.Header().Get("Retry-After") == "" {
			t.Errorf("%d Retry-After %q", r.Code, r.Header().Get("Retry-After"))
		}
	})
	t.Run("SecurityHeaders(): X-Frame-Options, X-Content-Type-Options, Referrer-Policy, HSTS (only over TLS)", func(t *testing.T) {
		a := aemeath.New()
		a.Use(aemeath.SecurityHeaders())
		aemeath.GET(a, "/", ok)
		h := do(a, "GET", "/", "").Header()
		for _, k := range []string{"X-Frame-Options", "X-Content-Type-Options", "Referrer-Policy"} {
			if h.Get(k) == "" {
				t.Errorf("%s missing", k)
			}
		}
		if h.Get("Strict-Transport-Security") != "" {
			t.Error("HSTS sent over plain http")
		}
	})
	t.Run("RequestID(): reads a safe X-Request-Id or makes one", func(t *testing.T) {
		a := aemeath.New()
		a.Use(aemeath.RequestID())
		aemeath.GET(a, "/", ok)
		if got := do(a, "GET", "/", "", "X-Request-Id", "abc-123").Header().Get("X-Request-Id"); got != "abc-123" {
			t.Errorf("safe id not kept: %q", got)
		}
		if got := do(a, "GET", "/", "", "X-Request-Id", "bad id\twith spaces").Header().Get("X-Request-Id"); got == "" || strings.Contains(got, " ") {
			t.Errorf("unsafe id kept: %q", got)
		}
	})
	t.Run("Gzip(): compresses JSON for clients that accept it; leaves tiny bodies alone", func(t *testing.T) {
		a := aemeath.New()
		a.Use(aemeath.Gzip())
		aemeath.GET(a, "/big", ok)
		aemeath.GET(a, "/tiny", func(_ *aemeath.Ctx, _ struct{}) (helloOut, error) { return helloOut{"x"}, nil })
		if enc := do(a, "GET", "/big", "", "Accept-Encoding", "gzip").Header().Get("Content-Encoding"); enc != "gzip" {
			t.Errorf("big: %q", enc)
		}
		if enc := do(a, "GET", "/tiny", "", "Accept-Encoding", "gzip").Header().Get("Content-Encoding"); enc != "" {
			t.Errorf("tiny: %q", enc)
		}
		if enc := do(a, "GET", "/big", "").Header().Get("Content-Encoding"); enc != "" {
			t.Errorf("client without gzip: %q", enc)
		}
	})
	t.Run("Recover(): answers 500", func(t *testing.T) {
		a := aemeath.New()
		a.Use(aemeath.Recover())
		aemeath.GET(a, "/", func(_ *aemeath.Ctx, _ struct{}) (struct{}, error) { panic("x") })
		if r := do(a, "GET", "/", ""); r.Code != 500 {
			t.Errorf("%d", r.Code)
		}
	})
}

func TestHealthAndStatic(t *testing.T) {
	a := aemeath.New()
	a.EnableHealth()
	fail := atomic.Bool{}
	a.AddReadyCheck("thing", func(context.Context) error {
		if fail.Load() {
			return errors.New("down")
		}
		return nil
	})
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "sub", "a.txt"), []byte("hi"), 0o600)
	a.Static("/public", dir)
	t.Run("/healthz and /readyz: 200, or 503 when a check fails", func(t *testing.T) {
		if do(a, "GET", "/healthz", "").Code != 200 || do(a, "GET", "/readyz", "").Code != 200 {
			t.Error("not 200")
		}
		fail.Store(true)
		if c := do(a, "GET", "/readyz", "").Code; c != 503 {
			t.Errorf("failing check: %d", c)
		}
		if c := do(a, "GET", "/healthz", "").Code; c != 200 {
			t.Errorf("liveness changed: %d", c)
		}
	})
	t.Run("Static: no directory listings", func(t *testing.T) {
		if r := do(a, "GET", "/public/sub/", ""); r.Code == 200 && strings.Contains(r.Body.String(), "a.txt") {
			t.Errorf("listing: %s", r.Body.String())
		}
		if r := do(a, "GET", "/public/sub/a.txt", ""); r.Code != 200 || r.Body.String() != "hi" {
			t.Errorf("file: %d", r.Code)
		}
	})
}

type address struct {
	City string `json:"city" validate:"required"`
}
type item struct {
	Qty int `json:"qty" validate:"gte=1"`
}
type nestedIn struct {
	Address address            `json:"address"`
	Items   []item             `json:"items"`
	Extra   map[string]address `json:"extra"`
	Kind    int                `json:"kind" validate:"oneof=1 2"`
	ID      string             `json:"id" validate:"uuid"`
}

func TestValidationClaims(t *testing.T) {
	a := aemeath.New()
	aemeath.POST(a, "/n", func(_ *aemeath.Ctx, in nestedIn) (struct{}, error) { return struct{}{}, nil })
	r := do(a, "POST", "/n", `{"address":{},"items":[{"qty":1},{"qty":0}],"extra":{"home":{}},"kind":0,"id":"00000000-0000-0000-0000-000000000000"}`)
	body := r.Body.String()
	t.Run("Nested fields are named address.city, items[1].qty, extra[home].city; all failures at once", func(t *testing.T) {
		for _, f := range []string{`"address.city"`, `"items[1].qty"`, `"extra[home].city"`} {
			if !strings.Contains(body, f) {
				t.Errorf("missing %s in %s", f, body)
			}
		}
	})
	t.Run(`{"kind": 0} fails oneof=1 2`, func(t *testing.T) {
		if !strings.Contains(body, `"field":"kind"`) {
			t.Errorf("%s", body)
		}
	})
	t.Run("uuid: 8-4-4-4-12 hex, any version", func(t *testing.T) {
		if strings.Contains(body, `"field":"id"`) {
			t.Errorf("nil UUID refused: %s", body)
		}
	})
}

type userIn struct {
	Name string `json:"name" validate:"required"`
}
type userOut struct {
	ID    int               `json:"id"`
	Tags  []string          `json:"tags"`
	Nick  *string           `json:"nick"`
	Bio   string            `json:"bio,omitempty"`
	Attrs map[string]string `json:"attrs"`
}

func TestGeneratorClaims(t *testing.T) {
	a := aemeath.New()
	aemeath.GET(a, "/hello/{name}", func(_ *aemeath.Ctx, in helloIn) (helloOut, error) { return helloOut{}, nil })
	aemeath.POST(a, "/users", func(_ *aemeath.Ctx, in userIn) (userOut, error) { return userOut{}, nil }, aemeath.RequireAuth())
	aemeath.PUT(a, "/places", func(_ *aemeath.Ctx, _ struct {
		Addr address `json:"addr"`
	}) (struct{}, error) {
		return struct{}{}, nil
	})
	aemeath.GET(a, "/users", func(_ *aemeath.Ctx, _ struct {
		Limit int `query:"limit"`
	}) ([]userOut, error) {
		return nil, nil
	})
	aemeath.GET(a, "/users/{id}", func(_ *aemeath.Ctx, _ struct {
		ID int `path:"id"`
	}) (userOut, error) {
		return userOut{}, nil
	})
	a.EnableDocs(aemeath.Info{Title: "t", Version: "1"})
	a.EnableClient()
	client := do(a, "GET", "/client.ts", "").Body.String()
	schemas := do(a, "GET", "/schemas.ts", "").Body.String()
	hooks := do(a, "GET", "/hooks.ts", "").Body.String()
	openapi := do(a, "GET", "/openapi.json", "").Body.String()

	has := func(t *testing.T, what, hay string, needles ...string) {
		t.Helper()
		for _, n := range needles {
			if !strings.Contains(hay, n) {
				t.Errorf("%s does not contain %q", what, n)
			}
		}
	}
	t.Run("function names come from the method and path (getHelloByName)", func(t *testing.T) {
		has(t, "client.ts", client, "getHelloByName", "postUsers", "getUsersById")
	})
	t.Run("schema names: postUsersBodySchema, getUsersQuerySchema, getUsersByIdPathSchema", func(t *testing.T) {
		has(t, "schemas.ts", schemas, "postUsersBodySchema", "getUsersQuerySchema", "getUsersByIdPathSchema")
	})
	t.Run("hooks: useGetUsers, usePostUsers", func(t *testing.T) {
		has(t, "hooks.ts", hooks, "useGetUsers", "usePostUsers", "useQuery", "useMutation")
	})
	t.Run("Output: nil slices and maps are T[] | null, pointers T | null, omitempty optional", func(t *testing.T) {
		for _, re := range []string{`tags: Array<string> \| null`, `nick: string \| null`, `bio\?: string`, `attrs: Record<string, string> \| null`} {
			if !regexp.MustCompile(re).MatchString(client) {
				t.Errorf("client.ts does not match %s", re)
			}
		}
	})
	t.Run("OpenAPI 3.0.3; input types are named <Type>Input; every operation documents 400/422/500, plus 401 with RequireAuth", func(t *testing.T) {
		var doc struct {
			OpenAPI    string `json:"openapi"`
			Components struct {
				Schemas map[string]any `json:"schemas"`
			} `json:"components"`
			Paths map[string]map[string]struct {
				Responses map[string]any `json:"responses"`
			} `json:"paths"`
		}
		if err := json.Unmarshal([]byte(openapi), &doc); err != nil {
			t.Fatal(err)
		}
		if doc.OpenAPI != "3.0.3" {
			t.Errorf("version %s", doc.OpenAPI)
		}
		// A named struct reached through an input becomes "<Type>Input"; the
		// top-level input struct itself is written into the operation, not as a component.
		if _, ok := doc.Components.Schemas["AddressInput"]; !ok {
			names := []string{}
			for k := range doc.Components.Schemas {
				names = append(names, k)
			}
			t.Errorf("no AddressInput component; have %v", names)
		}
		if _, ok := doc.Components.Schemas["UserInInput"]; ok {
			t.Error("the top-level input struct is a component now; update the README, which says it is inlined")
		}
		for p, ops := range doc.Paths {
			for m, op := range ops {
				for _, code := range []string{"400", "422", "500"} {
					if _, ok := op.Responses[code]; !ok {
						t.Errorf("%s %s does not document %s", m, p, code)
					}
				}
				_, has401 := op.Responses["401"]
				if want := m == "post" && p == "/users"; has401 != want {
					t.Errorf("%s %s: 401 documented = %v", m, p, has401)
				}
			}
		}
	})
}

type flagsIn struct {
	Active bool  `query:"active"`
	Page   int   `query:"page"`
	Force  *bool `query:"force"`
}
type flagsOut struct {
	Query string `json:"query"`
}

func TestGoClientClaims(t *testing.T) {
	ep := aemeath.NewEndpoint[flagsIn, flagsOut]("GET", "/flags")
	a := aemeath.New()
	aemeath.Register(a, ep, func(c *aemeath.Ctx, _ flagsIn) (flagsOut, error) { return flagsOut{Query: c.R.URL.RawQuery}, nil })
	f := false
	out, err := ep.Call(context.Background(), aetest.NewClient(a), flagsIn{Force: &f})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("Zero values of non-pointer parameters are omitted; a pointer sends an explicit false", func(t *testing.T) {
		if out.Query != "force=false" {
			t.Errorf("query sent: %q", out.Query)
		}
	})
	t.Run("Responses with status >= 400 are *aemeath.HTTPError with Errors for validation failures", func(t *testing.T) {
		ep2 := aemeath.NewEndpoint[userIn, userOut]("POST", "/u")
		aemeath.Register(a, ep2, func(_ *aemeath.Ctx, _ userIn) (userOut, error) { return userOut{}, nil })
		_, err := ep2.Call(context.Background(), aetest.NewClient(a), userIn{})
		var he *aemeath.HTTPError
		if !errors.As(err, &he) || he.Code != 422 || len(he.Errors) == 0 {
			t.Errorf("%v", err)
		}
	})
}

func TestSessionClaims(t *testing.T) {
	a := aemeath.New()
	a.Use(session.Middleware(session.Config{Store: session.NewMemoryStore()}))
	a.Handle("GET", "/anon", func(c *aemeath.Ctx) error { return c.Text(200, "ok") })
	a.Handle("GET", "/login", func(c *aemeath.Ctx) error { session.From(c).Set("u", "ana"); return c.Text(200, "ok") })
	t.Run("A cookie is only sent when the session was touched", func(t *testing.T) {
		if got := do(a, "GET", "/anon", "").Header().Get("Set-Cookie"); got != "" {
			t.Errorf("anonymous visitor got %q", got)
		}
	})
	t.Run("random 43-character id in an HttpOnly, Secure, SameSite=Lax cookie", func(t *testing.T) {
		cs := do(a, "GET", "/login", "").Result().Cookies()
		if len(cs) != 1 || len(cs[0].Value) != 43 || !cs[0].HttpOnly || !cs[0].Secure || cs[0].SameSite != http.SameSiteLaxMode {
			t.Errorf("%+v", cs)
		}
	})
	t.Run("An id the server did not issue is never adopted", func(t *testing.T) {
		planted := strings.Repeat("A", 43)
		cs := do(a, "GET", "/login", "", "Cookie", "aemeath_session="+planted).Result().Cookies()
		if len(cs) != 1 || cs[0].Value == planted {
			t.Errorf("%+v", cs)
		}
	})
}

type uploadIn struct {
	Name   string                `form:"name"`
	Avatar *multipart.FileHeader `form:"avatar" accept:"image/png" maxsize:"1MB"`
}

func TestFormClaims(t *testing.T) {
	a := aemeath.New()
	aemeath.POST(a, "/up", func(_ *aemeath.Ctx, in uploadIn) (helloOut, error) { return helloOut{in.Name}, nil })
	post := func(target string, files map[string][]byte, n int) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		for i := 0; i < n; i++ {
			for name, content := range files {
				fw, _ := w.CreateFormFile("avatar", name)
				_, _ = fw.Write(content)
			}
		}
		_ = w.Close()
		req := httptest.NewRequest("POST", target, &buf)
		req.Header.Set("Content-Type", w.FormDataContentType())
		rec := httptest.NewRecorder()
		a.ServeHTTP(rec, req)
		return rec
	}
	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 64))
	t.Run("The type is detected from the file's bytes: a script renamed to .png is rejected", func(t *testing.T) {
		if r := post("/up", map[string][]byte{"a.png": png}, 1); r.Code != 200 {
			t.Errorf("real png: %d %s", r.Code, r.Body.String())
		}
		r := post("/up", map[string][]byte{"evil.png": []byte("#!/bin/sh\necho hi\n")}, 1)
		if r.Code != 422 || !strings.Contains(r.Body.String(), `"rule":"accept"`) {
			t.Errorf("script: %d %s", r.Code, r.Body.String())
		}
	})
	t.Run("A *multipart.FileHeader field takes exactly one file (a second one is a 422)", func(t *testing.T) {
		if r := post("/up", map[string][]byte{"a.png": png}, 2); r.Code != 422 {
			t.Errorf("%d %s", r.Code, r.Body.String())
		}
	})
	t.Run("Form values come from the request body only, never from the URL query", func(t *testing.T) {
		if r := post("/up?name=attacker", map[string][]byte{"a.png": png}, 1); strings.Contains(r.Body.String(), "attacker") {
			t.Errorf("%s", r.Body.String())
		}
	})
}

func TestStorageClaims(t *testing.T) {
	dir := t.TempDir()
	name, err := aemeath.NewLocalStorage(dir).Save("../../etc/My File.png", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("Save returns <unix>_<random>_<original> and strips any directory part; mode 0600", func(t *testing.T) {
		if !regexp.MustCompile(`^\d+_[0-9a-f]+_My File\.png$`).MatchString(name) && !regexp.MustCompile(`^\d+_[0-9a-f]+_My.File\.png$`).MatchString(name) {
			t.Errorf("stored name %q", name)
		}
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("%v %v", st, err)
		}
	})
}

type flaky struct{ runs *atomic.Int32 }

func (f flaky) Run(context.Context) error { f.runs.Add(1); return errors.New("no") }

func TestJobsClaims(t *testing.T) {
	t.Run("A job that returns an error is retried (3 attempts by default)", func(t *testing.T) {
		var n atomic.Int32
		r := jobs.New(jobs.WithBackoff(func(int) time.Duration { return time.Millisecond }))
		r.Start()
		if err := r.Enqueue(flaky{&n}); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(3 * time.Second)
		for r.Stats().Failed == 0 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		_ = r.Shutdown(context.Background())
		if n.Load() != 3 {
			t.Errorf("%d attempts", n.Load())
		}
	})
	t.Run("backoff 1 s, 2 s, ..., capped at one minute", func(t *testing.T) {
		b := jobs.Exponential(time.Second)
		if b(1) != time.Second || b(2) != 2*time.Second || b(30) != time.Minute {
			t.Errorf("%v %v %v", b(1), b(2), b(30))
		}
	})
	t.Run("Enqueue never blocks; jobs.ErrQueueFull when there is no room", func(t *testing.T) {
		r := jobs.New(jobs.WithQueueSize(1)) // not started: nothing drains the queue
		_ = r.Enqueue(jobs.Func(func(context.Context) error { return nil }))
		done := make(chan error, 1)
		go func() { done <- r.Enqueue(jobs.Func(func(context.Context) error { return nil })) }()
		select {
		case err := <-done:
			if !errors.Is(err, jobs.ErrQueueFull) {
				t.Errorf("%v", err)
			}
		case <-time.After(time.Second):
			t.Error("Enqueue blocked")
		}
	})
}

func TestMetricsClaims(t *testing.T) {
	reg := metrics.New()
	a := aemeath.New()
	a.Use(reg.Middleware())
	a.Handle("GET", "/metrics", reg.Handler())
	aemeath.GET(a, "/users/{id}", func(_ *aemeath.Ctx, _ struct {
		ID int `path:"id"`
	}) (struct{}, error) {
		return struct{}{}, nil
	})
	do(a, "GET", "/users/42", "")
	do(a, "GET", "/wp-admin/x1", "")
	do(a, "GET", "/wp-admin/x2", "")
	out := do(a, "GET", "/metrics", "").Body.String()
	t.Run("route label is the pattern, never the raw path; unmatched requests share one series", func(t *testing.T) {
		if !strings.Contains(out, `route="/users/{id}"`) || strings.Contains(out, "/users/42") || strings.Contains(out, "wp-admin") || !strings.Contains(out, `route="unmatched"`) {
			t.Errorf("%s", out)
		}
	})
	t.Run("http_requests_total, http_request_duration_seconds, http_requests_in_flight", func(t *testing.T) {
		for _, m := range []string{"http_requests_total{", "http_request_duration_seconds_bucket{", "http_requests_in_flight"} {
			if !strings.Contains(out, m) {
				t.Errorf("missing %s", m)
			}
		}
	})
}

type formatsIn struct {
	Email string `json:"email" validate:"email"`
	Site  string `json:"site" validate:"url"`
}

func TestFormatClaims(t *testing.T) {
	a := aemeath.New()
	aemeath.POST(a, "/f", func(_ *aemeath.Ctx, _ formatsIn) (struct{}, error) { return struct{}{}, nil })
	bad := func(field, body string) bool {
		return strings.Contains(do(a, "POST", "/f", body).Body.String(), `"field":"`+field+`"`)
	}
	t.Run("email: local@domain.tld, non-ASCII letters allowed, a@b is refused", func(t *testing.T) {
		if bad("email", `{"email":"ana@example.com"}`) {
			t.Error("ana@example.com refused")
		}
		if bad("email", `{"email":"안나@例え.jp"}`) {
			t.Error("non-ASCII address refused")
		}
		if !bad("email", `{"email":"a@b"}`) {
			t.Error("a@b accepted")
		}
	})
	t.Run("url: absolute http/https", func(t *testing.T) {
		if bad("site", `{"site":"https://example.com/x?y=1"}`) {
			t.Error("https URL refused")
		}
		for _, u := range []string{"example.com", "/relative", "javascript:alert(1)", "ftp://example.com", "mailto:a@b.com"} {
			if !bad("site", `{"site":"`+u+`"}`) {
				t.Errorf("%s accepted", u)
			}
		}
	})
	t.Run("the generated Zod schemas and OpenAPI use the same patterns as the server", func(t *testing.T) {
		a.EnableDocs(aemeath.Info{Title: "t", Version: "1"})
		a.EnableClient()
		schemas := do(a, "GET", "/schemas.ts", "").Body.String()
		openapi := do(a, "GET", "/openapi.json", "").Body.String()
		for _, want := range []string{"@", "http"} {
			if !strings.Contains(schemas, want) {
				t.Errorf("schemas.ts has no %q", want)
			}
		}
		if !strings.Contains(schemas, ".regex(") || !strings.Contains(openapi, `"pattern"`) {
			t.Errorf("no shared pattern: zod regex %v, openapi pattern %v",
				strings.Contains(schemas, ".regex("), strings.Contains(openapi, `"pattern"`))
		}
	})
}

func TestCSRFTokenIsLazy(t *testing.T) {
	a := aemeath.New()
	a.Use(session.Middleware(session.Config{Store: session.NewMemoryStore()}), csrf.Middleware(csrf.Config{}))
	a.Handle("GET", "/page", func(c *aemeath.Ctx) error { return c.Text(200, "page") })
	a.Handle("GET", "/token", func(c *aemeath.Ctx) error { return c.Text(200, csrf.Token(c)) })
	t.Run("The token is only created when something asks for it, so crawlers get no session", func(t *testing.T) {
		if got := do(a, "GET", "/page", "").Header().Get("Set-Cookie"); got != "" {
			t.Errorf("a visitor who never asked for a token got %q", got)
		}
		r := do(a, "GET", "/token", "")
		if r.Header().Get("Set-Cookie") == "" || r.Body.Len() == 0 {
			t.Errorf("asking for a token gave no session: %q %q", r.Header().Get("Set-Cookie"), r.Body.String())
		}
	})
	t.Run("Requests with a missing or wrong token get 403", func(t *testing.T) {
		a.Handle("POST", "/save", func(c *aemeath.Ctx) error { return c.Text(200, "saved") })
		if r := do(a, "POST", "/save", ""); r.Code != 403 {
			t.Errorf("no token: %d", r.Code)
		}
		if r := do(a, "POST", "/save", "", "X-CSRF-Token", "wrong"); r.Code != 403 {
			t.Errorf("wrong token: %d", r.Code)
		}
	})
}
