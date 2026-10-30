package aemeath_test

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
)

type none struct{}

func get(h http.Handler, target string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", target, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	req.RemoteAddr = "1.2.3.4:5678"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func mw(name string) aemeath.Middleware {
	return func(next aemeath.HandlerFunc) aemeath.HandlerFunc {
		return func(c *aemeath.Ctx) error {
			c.W.Header().Add("X-Mw", name)
			return next(c)
		}
	}
}

func okRoute(*aemeath.Ctx, none) (none, error) { return none{}, nil }

func TestMethodNotAllowedIs405(t *testing.T) {
	app := aemeath.New()
	aemeath.GET(app, "/x", okRoute)
	rec := do(app, "POST", "/x", "")
	if rec.Code != 405 {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); !strings.Contains(allow, "GET") {
		t.Fatalf("Allow = %q, want it to contain GET", allow)
	}
	if rec := do(app, "GET", "/missing", ""); rec.Code != 404 {
		t.Fatalf("unknown path: status = %d, want 404", rec.Code)
	}
}

func TestSiblingGroupsDoNotShareMiddlewares(t *testing.T) {
	app := aemeath.New()
	parent := app.Group("/p")
	parent.Use(mw("a"))
	parent.Use(mw("b"))
	parent.Use(mw("c")) // len 3, cap 4: the aliasing trap
	one := parent.Group("/one", mw("ONE"))
	two := parent.Group("/two", mw("TWO"))
	aemeath.GroupGET(one, "/h", okRoute)
	aemeath.GroupGET(two, "/h", okRoute)

	got1 := strings.Join(get(app, "/p/one/h").Header()["X-Mw"], ",")
	got2 := strings.Join(get(app, "/p/two/h").Header()["X-Mw"], ",")
	if got1 != "a,b,c,ONE" || got2 != "a,b,c,TWO" {
		t.Fatalf("one = %q, two = %q", got1, got2)
	}
}

func TestGroupUseAfterRouteStillApplies(t *testing.T) {
	app := aemeath.New()
	g := app.Group("/g")
	aemeath.GroupGET(g, "/h", okRoute)
	g.Use(mw("late"))
	if got := get(app, "/g/h").Header().Get("X-Mw"); got != "late" {
		t.Fatalf("X-Mw = %q, want late", got)
	}
}

func TestGroupRootIsExactMatch(t *testing.T) {
	app := aemeath.New()
	g := app.Group("/admin")
	aemeath.GroupGET(g, "/", okRoute)
	if c := get(app, "/admin").Code; c != 200 {
		t.Fatalf("/admin = %d, want 200", c)
	}
	if c := get(app, "/admin/anything/else").Code; c != 404 {
		t.Fatalf("/admin/anything/else = %d, want 404 (must not be a catch-all)", c)
	}
}

func TestRateLimiterWindowResets(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.RateLimiter(2, 200*time.Millisecond))
	app.Handle("GET", "/p", func(c *aemeath.Ctx) error { return c.JSON(200, "ok") })

	// Requests at t=0, 150ms (both allowed), then 300ms: the window that
	// started at t=0 has expired, so the third request must be allowed.
	var codes []int
	for i := 0; i < 3; i++ {
		codes = append(codes, get(app, "/p").Code)
		time.Sleep(150 * time.Millisecond)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 200 {
		t.Fatalf("codes = %v, want [200 200 200]", codes)
	}
}

func TestRateLimiterSetsRetryAfter(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.RateLimiter(1, time.Minute))
	app.Handle("GET", "/p", func(c *aemeath.Ctx) error { return c.JSON(200, "ok") })
	get(app, "/p")
	rec := get(app, "/p")
	if rec.Code != 429 || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("code = %d, Retry-After = %q", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestClientSendsPointerToZeroValues(t *testing.T) {
	type in struct {
		Page *int  `query:"page"`
		On   *bool `query:"on"`
	}
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	zero, no := 0, false
	ep := aemeath.NewEndpoint[in, none]("GET", "/q")
	if _, err := ep.Call(context.Background(), aemeath.NewClient(srv.URL), in{Page: &zero, On: &no}); err != nil {
		t.Fatal(err)
	}
	if got != "on=false&page=0" {
		t.Fatalf("query = %q, want on=false&page=0", got)
	}
}

func TestQuerySlices(t *testing.T) {
	type in struct {
		Tags []string `query:"tag"`
		IDs  []int    `query:"id"`
	}
	type out struct {
		Tags []string `json:"tags"`
		IDs  []int    `json:"ids"`
	}
	app := aemeath.New()
	aemeath.GET(app, "/s", func(_ *aemeath.Ctx, i in) (out, error) { return out(i), nil })

	rec := get(app, "/s?tag=a&tag=b&id=1&id=2")
	want := `{"tags":["a","b"],"ids":[1,2]}`
	if strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("body = %s, want %s", rec.Body, want)
	}
	if c := get(app, "/s?id=x").Code; c != 400 {
		t.Fatalf("invalid slice element: status = %d, want 400", c)
	}

	// Round trip through the Go client.
	srv := httptest.NewServer(app)
	defer srv.Close()
	ep := aemeath.NewEndpoint[in, out]("GET", "/s")
	res, err := ep.Call(context.Background(), aemeath.NewClient(srv.URL), in{Tags: []string{"x", "y"}, IDs: []int{7}})
	if err != nil || len(res.Tags) != 2 || res.IDs[0] != 7 {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
}

func TestRequiredSliceAndMapAreAllowed(t *testing.T) {
	type in struct {
		Tags []string          `json:"tags" validate:"required"`
		Meta map[string]string `json:"meta" validate:"required"`
	}
	app := aemeath.New()
	aemeath.POST(app, "/x", func(*aemeath.Ctx, in) (none, error) { return none{}, nil })
	if c := do(app, "POST", "/x", `{}`).Code; c != 422 {
		t.Fatalf("missing: status = %d, want 422", c)
	}
	if c := do(app, "POST", "/x", `{"tags":["a"],"meta":{"k":"v"}}`).Code; c != 200 {
		t.Fatalf("present: status = %d, want 200", c)
	}
}

type category struct {
	Name     string      `json:"name" validate:"required"`
	Children []*category `json:"children"`
	Parent   *category   `json:"parent"`
}

func TestRecursiveTypesDoNotOverflow(t *testing.T) {
	app := aemeath.New()
	aemeath.POST(app, "/c", func(_ *aemeath.Ctx, in category) (category, error) { return in, nil })
	if c := do(app, "POST", "/c", `{"name":"root"}`).Code; c != 200 {
		t.Fatalf("status = %d, want 200", c)
	}
	_ = app.OpenAPI(aemeath.Info{Title: "t"})
	_ = app.TypeScriptClient()
}

func TestValidationNamesParamsByTheirTag(t *testing.T) {
	type in struct {
		Search string `query:"q" validate:"required"`
	}
	app := aemeath.New()
	aemeath.GET(app, "/s", func(*aemeath.Ctx, in) (none, error) { return none{}, nil })
	if body := get(app, "/s").Body.String(); !strings.Contains(body, `"field":"q"`) || !strings.Contains(body, "is required") {
		t.Fatalf("body = %s", body)
	}
}

func TestBodyTooLargeIs413(t *testing.T) {
	old := aemeath.DefaultMaxBodyBytes
	aemeath.DefaultMaxBodyBytes = 10
	defer func() { aemeath.DefaultMaxBodyBytes = old }()
	rec := do(newApp(), "POST", "/users", `{"name":"`+strings.Repeat("a", 100)+`"}`)
	if rec.Code != 413 {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestMaxBodySizeMiddlewareIs413(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.MaxBodySize(10))
	aemeath.POST(app, "/users", func(_ *aemeath.Ctx, in userIn) (userOut, error) { return userOut{Name: in.Name}, nil })
	rec := do(app, "POST", "/users", `{"name":"`+strings.Repeat("a", 100)+`"}`)
	if rec.Code != 413 {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestNonJSONContentTypeIs415(t *testing.T) {
	req := httptest.NewRequest("POST", "/users", strings.NewReader("name=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	newApp().ServeHTTP(rec, req)
	if rec.Code != 415 {
		t.Fatalf("status = %d, want 415", rec.Code)
	}
	req = httptest.NewRequest("POST", "/users", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	rec = httptest.NewRecorder()
	newApp().ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("json with charset: status = %d, want 201", rec.Code)
	}
}

func TestStatus204HasNoBody(t *testing.T) {
	app := aemeath.New()
	aemeath.DELETE(app, "/x/{id}", func(*aemeath.Ctx, struct {
		ID string `path:"id"`
	}) (none, error) {
		return none{}, nil
	}, aemeath.Status(204))
	rec := do(app, "DELETE", "/x/1", "")
	if rec.Code != 204 || rec.Body.Len() != 0 {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
}

func TestStaticHidesDirectoryListing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secret-backup.sql"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	app := aemeath.New()
	app.Static("/public", dir)
	rec := get(app, "/public/")
	if rec.Code != 404 || strings.Contains(rec.Body.String(), "secret-backup.sql") {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if c := get(app, "/public/secret-backup.sql").Code; c != 200 {
		t.Fatalf("direct file access: status = %d, want 200", c)
	}
}

func TestStaticGoesThroughMiddlewares(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0o644)
	app := aemeath.New()
	app.Use(aemeath.SecurityHeaders())
	app.Static("/s", dir)
	if get(app, "/s/a.txt").Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("global middleware did not run for static files")
	}
}

func TestCORSCredentialsWithWildcardPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	cfg := aemeath.DefaultCORSConfig()
	cfg.AllowCredentials = true
	aemeath.CORS(cfg)
}

func TestCORSPartialConfigKeepsDefaultMethodsAndHeaders(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.CORS(aemeath.CORSConfig{AllowOrigins: []string{"https://a.com"}, AllowCredentials: true}))
	req := httptest.NewRequest("OPTIONS", "/anything", nil)
	req.Header.Set("Origin", "https://a.com")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://a.com" ||
		rec.Header().Get("Access-Control-Allow-Methods") == "" ||
		rec.Header().Get("Access-Control-Allow-Headers") == "" ||
		rec.Header().Get("Vary") != "Origin" {
		t.Fatalf("headers = %v", rec.Header())
	}
	// A different origin gets no CORS headers.
	req = httptest.NewRequest("OPTIONS", "/anything", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unexpected CORS headers for a foreign origin: %v", rec.Header())
	}
}

func TestLocalStorageNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	s := aemeath.NewLocalStorage(dir)
	a, err := s.Save("avatar.png", strings.NewReader("USER-A"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Save("avatar.png", strings.NewReader("USER-B"))
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two uploads got the same stored name")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, a)); string(data) != "USER-A" {
		t.Fatalf("first upload was overwritten: %q", data)
	}
}

func TestLocalStorageStaysInsideUploadDir(t *testing.T) {
	dir := t.TempDir()
	s := aemeath.NewLocalStorage(filepath.Join(dir, "uploads"))
	for _, name := range []string{"../../evil.txt", `..\..\evil.txt`, "/etc/passwd", "..", ""} {
		stored, err := s.Save(name, strings.NewReader("x"))
		if err != nil {
			t.Fatalf("Save(%q): %v", name, err)
		}
		if strings.ContainsAny(stored, `/\`) {
			t.Fatalf("Save(%q) returned a path: %q", name, stored)
		}
		if _, err := os.Stat(filepath.Join(dir, "uploads", stored)); err != nil {
			t.Fatalf("Save(%q): file not inside upload dir: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.txt")); err == nil {
		t.Fatal("file escaped the upload directory")
	}
	if !s.IsAllowedFile("PHOTO.PNG", []string{".png"}) {
		t.Fatal("extension check should be case-insensitive")
	}
}

func TestLoggerKeepsFlusher(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.Logger())
	var ok bool
	app.Handle("GET", "/sse", func(c *aemeath.Ctx) error {
		_, ok = c.W.(http.Flusher)
		return nil
	})
	get(app, "/sse")
	if !ok {
		t.Fatal("the ResponseWriter behind Logger() must implement http.Flusher")
	}
}

func TestEmbeddedStructsAreFlattenedInSchemas(t *testing.T) {
	type Base struct {
		ID int `json:"id"`
	}
	type Item struct {
		Base
		Name string `json:"name"`
	}
	app := aemeath.New()
	aemeath.GET(app, "/i", func(*aemeath.Ctx, none) (Item, error) { return Item{}, nil })

	ts := app.TypeScriptClient()
	if !strings.Contains(ts, "id: number;") || strings.Contains(ts, "Base:") {
		t.Fatalf("TypeScript output does not flatten the embedded struct:\n%s", ts)
	}
	doc := app.OpenAPI(aemeath.Info{})
	ref := doc["paths"].(map[string]any)["/i"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["$ref"].(string)
	schema := doc["components"].(map[string]any)["schemas"].(map[string]any)[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
	props := schema["properties"].(map[string]any)
	if _, ok := props["id"]; !ok {
		t.Fatalf("OpenAPI schema is missing the flattened field id: %v", props)
	}
	if _, ok := props["Base"]; ok {
		t.Fatalf("OpenAPI schema has a bogus Base property: %v", props)
	}
}

func TestUnmatchedRequestsPassThroughMiddlewares(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.SecurityHeaders(), aemeath.CORS())
	aemeath.GET(app, "/x", okRoute)

	for _, tc := range []struct {
		method, path string
		want         int
	}{{"GET", "/missing", 404}, {"POST", "/x", 405}} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Origin", "https://a.com")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("%s %s: status = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("%s %s: middlewares did not run: %v", tc.method, tc.path, rec.Header())
		}
	}
}

func TestPlainOptionsOnKnownPathIs204(t *testing.T) {
	app := aemeath.New()
	aemeath.GET(app, "/x", okRoute)
	rec := do(app, "OPTIONS", "/x", "")
	if rec.Code != 204 || rec.Header().Get("Allow") == "" {
		t.Fatalf("status = %d, Allow = %q", rec.Code, rec.Header().Get("Allow"))
	}
	if rec := do(app, "OPTIONS", "/missing", ""); rec.Code != 404 {
		t.Fatalf("OPTIONS on an unknown path: status = %d, want 404", rec.Code)
	}
}

func TestNilSlicesAreTypedAsNullable(t *testing.T) {
	type Item struct {
		Tags []string `json:"tags"`
	}
	app := aemeath.New()
	aemeath.GET(app, "/i", func(*aemeath.Ctx, none) (Item, error) { return Item{}, nil })
	aemeath.GET(app, "/l", func(*aemeath.Ctx, none) ([]Item, error) { return nil, nil })

	// What the server really sends for a nil slice:
	if body := strings.TrimSpace(get(app, "/i").Body.String()); body != `{"tags":null}` {
		t.Fatalf("body = %s", body)
	}
	ts := app.TypeScriptClient()
	if !strings.Contains(ts, "tags: Array<string> | null;") {
		t.Fatalf("TypeScript output should mark nil-able slices as nullable:\n%s", ts)
	}
	if !strings.Contains(ts, "request<Array<Item> | null>") {
		t.Fatalf("TypeScript output should mark a nil-able top-level slice as nullable:\n%s", ts)
	}
}

// log.Fatal(app.Listen(...)) prints "<nil>" and exits with status 1 after a
// graceful shutdown, so a clean stop looks like a crash to Docker and CI.
//
// Go files are checked as a whole. Markdown files are checked only inside
// fenced code blocks: a sentence that warns against the idiom may mention it.
func TestDocsAndExamplesDoNotWrapListenInLogFatal(t *testing.T) {
	idiom := regexp.MustCompile(`log\.Fatal\(\s*(app|a)\.(Listen|Serve|ListenTLS)\(`)
	fence := regexp.MustCompile("(?s)```.*?```")

	checked := 0
	for _, root := range []string{"README.md", "docs", "examples"} {
		if _, err := os.Stat(root); err != nil {
			t.Fatalf("%s not found: this test must run from the repository root, otherwise it checks nothing", root)
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "node_modules" || d.Name() == "generated" {
					return filepath.SkipDir
				}
				return nil
			}
			ext := filepath.Ext(path)
			if ext != ".go" && ext != ".md" {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			checked++
			text := string(b)
			if ext == ".md" {
				text = strings.Join(fence.FindAllString(text, -1), "\n")
			}
			if idiom.MatchString(text) {
				t.Errorf("%s wraps Listen in log.Fatal: use `if err := app.Listen(...); err != nil { log.Fatal(err) }`", path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	if checked < 5 {
		t.Fatalf("only %d files were checked; the test is not looking at the docs and examples", checked)
	}
}

func TestLocalStorageFilesArePrivateByDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes")
	}
	dir := filepath.Join(t.TempDir(), "uploads")
	s := aemeath.NewLocalStorage(dir)
	name, err := s.Save("a.txt", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(dir, name)); info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %o, want 600", info.Mode().Perm())
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o750 {
		t.Fatalf("directory mode = %o, want 750", info.Mode().Perm())
	}

	// A reverse proxy that must read the files can opt in.
	open := &aemeath.LocalStorage{UploadDir: filepath.Join(t.TempDir(), "pub"), FileMode: 0o644, DirMode: 0o755}
	name, err = open.Save("b.txt", strings.NewReader("y"))
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(open.UploadDir, name)); info.Mode().Perm() != 0o644 {
		t.Fatalf("configured file mode = %o, want 644", info.Mode().Perm())
	}
}
