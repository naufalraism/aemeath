package csrf_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/csrf"
	"github.com/naufalraism/aemeath/session"
)

type none struct{}
type tok struct {
	Token string `json:"token"`
}

func newApp(cfg csrf.Config) *aemeath.App {
	app := aemeath.New()
	// An origin trusted for writes must be trusted by the session middleware too.
	app.Use(session.Middleware(session.Config{Store: session.NewMemoryStore(), Insecure: true, TrustedOrigins: cfg.TrustedOrigins}))
	app.Use(csrf.Middleware(cfg))
	aemeath.GET(app, "/token", func(c *aemeath.Ctx, _ none) (tok, error) { return tok{csrf.Token(c)}, nil })
	aemeath.POST(app, "/act", func(c *aemeath.Ctx, _ none) (tok, error) { return tok{"done"}, nil })
	return app
}

func call(app *aemeath.App, method, path, sess string, h map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(""))
	r.Host = "app.test"
	if sess != "" {
		r.AddCookie(&http.Cookie{Name: "aemeath_session", Value: sess})
	}
	for k, v := range h {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}

func start(app *aemeath.App) (sess, token string) {
	w := call(app, "GET", "/token", "", nil)
	for _, c := range w.Result().Cookies() {
		if c.Name == "aemeath_session" {
			sess = c.Value
		}
	}
	b := w.Body.String()
	i := strings.Index(b, `"token":"`) + 9
	token = b[i : i+43]
	return
}

func TestSafeMethodsPassUnsafeNeedToken(t *testing.T) {
	app := newApp(csrf.Config{})
	sess, token := start(app)
	if w := call(app, "POST", "/act", sess, nil); w.Code != 403 {
		t.Fatalf("no token: %d", w.Code)
	}
	if w := call(app, "POST", "/act", sess, map[string]string{"X-CSRF-Token": "wrong"}); w.Code != 403 {
		t.Fatalf("wrong token: %d", w.Code)
	}
	if w := call(app, "POST", "/act", "", map[string]string{"X-CSRF-Token": token}); w.Code != 403 {
		t.Fatalf("token without its session: %d", w.Code)
	}
	if w := call(app, "POST", "/act", sess, map[string]string{"X-CSRF-Token": token}); w.Code != 200 {
		t.Fatalf("valid: %d %s", w.Code, w.Body)
	}
}

func TestOriginCheck(t *testing.T) {
	app := newApp(csrf.Config{TrustedOrigins: []string{"https://ok.example"}})
	sess, token := start(app)
	h := func(o string) map[string]string { return map[string]string{"X-CSRF-Token": token, "Origin": o} }
	if w := call(app, "POST", "/act", sess, h("https://evil.example")); w.Code != 403 {
		t.Fatalf("evil origin with valid token: %d", w.Code)
	}
	if w := call(app, "POST", "/act", sess, h("http://app.test")); w.Code != 200 {
		t.Fatalf("same origin: %d", w.Code)
	}
	if w := call(app, "POST", "/act", sess, h("https://ok.example")); w.Code != 200 {
		t.Fatalf("trusted origin: %d", w.Code)
	}
}

func TestOptionalCookie(t *testing.T) {
	app := newApp(csrf.Config{Cookie: "XSRF-TOKEN", Insecure: true})
	_, token := start(app)
	w := call(app, "GET", "/token", "", nil)
	found := false
	for _, c := range w.Result().Cookies() {
		if c.Name == "XSRF-TOKEN" && c.Value != "" {
			found = true
		}
	}
	if !found || token == "" {
		t.Fatal("XSRF cookie missing")
	}
}

// Visitors who never ask for a token (crawlers, bots, health checks) must not
// get a session: they used to fill the store until real users could no longer
// log in.
func TestNoSessionForVisitorsWithoutToken(t *testing.T) {
	store := session.NewMemoryStore().WithMaxSessions(3)
	app := aemeath.New()
	app.Use(session.Middleware(session.Config{Store: store, Insecure: true}), csrf.Middleware(csrf.Config{}))
	aemeath.GET(app, "/", func(c *aemeath.Ctx, _ none) (tok, error) { return tok{"home"}, nil })
	aemeath.GET(app, "/token", func(c *aemeath.Ctx, _ none) (tok, error) { return tok{csrf.Token(c)}, nil })
	aemeath.POST(app, "/act", func(c *aemeath.Ctx, _ none) (tok, error) { return tok{"done"}, nil })

	for i := 0; i < 10; i++ {
		if w := call(app, "GET", "/", "", nil); w.Header().Get("Set-Cookie") != "" {
			t.Fatalf("anonymous GET %d set a cookie: %s", i, w.Header().Get("Set-Cookie"))
		}
		if w := call(app, "POST", "/act", "", nil); w.Code != 403 {
			t.Fatalf("anonymous POST without token: %d", w.Code)
		}
	}
	if n := store.Len(); n != 0 {
		t.Fatalf("store holds %d sessions after anonymous traffic, want 0", n)
	}

	// A real visitor still gets a token, a session, and can act.
	sess, token := start(app)
	if sess == "" || token == "" {
		t.Fatal("asking for the token must create the session")
	}
	if w := call(app, "POST", "/act", sess, map[string]string{"X-CSRF-Token": token}); w.Code != 200 {
		t.Fatalf("valid request: %d %s", w.Code, w.Body)
	}
}

// HTML forms cannot send custom headers, so a form POST was always rejected.
// The token is now also accepted from the form body, which the route can
// still bind afterwards.
func TestTokenFromFormField(t *testing.T) {
	app := aemeath.New()
	app.Use(session.Middleware(session.Config{Store: session.NewMemoryStore(), Insecure: true}), csrf.Middleware(csrf.Config{}))
	aemeath.GET(app, "/token", func(c *aemeath.Ctx, _ none) (tok, error) { return tok{csrf.Token(c)}, nil })
	type note struct {
		Title string `form:"title" validate:"required"`
	}
	aemeath.POST(app, "/notes", func(c *aemeath.Ctx, in note) (tok, error) { return tok{in.Title}, nil })
	sess, token := start(app)

	post := func(contentType, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/notes", strings.NewReader(body))
		r.Host = "app.test"
		r.Header.Set("Content-Type", contentType)
		r.AddCookie(&http.Cookie{Name: "aemeath_session", Value: sess})
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w
	}

	if w := post("application/x-www-form-urlencoded", "title=hello&csrf_token="+token); w.Code != 200 || !strings.Contains(w.Body.String(), "hello") {
		t.Fatalf("urlencoded with token: %d %s", w.Code, w.Body)
	}
	if w := post("application/x-www-form-urlencoded", "title=hello&csrf_token=wrong"); w.Code != 403 {
		t.Fatalf("urlencoded with a wrong token: %d", w.Code)
	}
	if w := post("application/x-www-form-urlencoded", "title=hello"); w.Code != 403 {
		t.Fatalf("urlencoded without token: %d", w.Code)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("title", "multi")
	_ = mw.WriteField("csrf_token", token)
	_ = mw.Close()
	if w := post(mw.FormDataContentType(), buf.String()); w.Code != 200 || !strings.Contains(w.Body.String(), "multi") {
		t.Fatalf("multipart with token: %d %s", w.Code, w.Body)
	}
}

// The token used to survive login (session.Renew kept it), although the guide
// says it changes: a token learned before login stayed valid afterwards.
func TestTokenChangesAtLogin(t *testing.T) {
	app := aemeath.New()
	app.Use(session.Middleware(session.Config{Store: session.NewMemoryStore(), Insecure: true}), csrf.Middleware(csrf.Config{}))
	aemeath.GET(app, "/token", func(c *aemeath.Ctx, _ none) (tok, error) { return tok{csrf.Token(c)}, nil })
	aemeath.POST(app, "/login", func(c *aemeath.Ctx, _ none) (tok, error) {
		s := session.From(c)
		if err := s.Renew(c.Context()); err != nil {
			return tok{}, err
		}
		s.Set("user", "ann")
		return tok{"in"}, nil
	})
	aemeath.POST(app, "/act", func(c *aemeath.Ctx, _ none) (tok, error) { return tok{"done"}, nil })

	sess, before := start(app)
	w := call(app, "POST", "/login", sess, map[string]string{"X-CSRF-Token": before})
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	var newSess string
	for _, c := range w.Result().Cookies() {
		if c.Name == "aemeath_session" {
			newSess = c.Value
		}
	}
	if code := call(app, "POST", "/act", newSess, map[string]string{"X-CSRF-Token": before}).Code; code != 403 {
		t.Fatalf("the token from before login still works after login: %d", code)
	}
	b := call(app, "GET", "/token", newSess, nil).Body.String()
	i := strings.Index(b, `"token":"`) + 9
	after := b[i : i+43]
	if after == before {
		t.Fatal("the token did not change at login")
	}
	if code := call(app, "POST", "/act", newSess, map[string]string{"X-CSRF-Token": after}).Code; code != 200 {
		t.Fatalf("the new token: %d", code)
	}
	// The user's own data survives Renew.
	if code := call(app, "POST", "/act", newSess, map[string]string{"X-CSRF-Token": after}).Code; code != 200 {
		t.Fatal("second request")
	}
}

// The Origin check compared only the host, so on an https server a page
// served over plain http on the same host (which an attacker on the network
// can modify) passed.
func TestOriginSchemeOnTLS(t *testing.T) {
	app := aemeath.New()
	app.Use(session.Middleware(session.Config{Store: session.NewMemoryStore()}), csrf.Middleware(csrf.Config{}))
	aemeath.GET(app, "/token", func(c *aemeath.Ctx, _ none) (tok, error) { return tok{csrf.Token(c)}, nil })
	aemeath.POST(app, "/act", func(c *aemeath.Ctx, _ none) (tok, error) { return tok{"done"}, nil })

	do := func(target, sess string, h map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", target, nil) // https:// target => r.TLS is set
		if h != nil {
			r.Method = "POST"
		}
		if sess != "" {
			r.AddCookie(&http.Cookie{Name: "__Host-aemeath_session", Value: sess})
		}
		for k, v := range h {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w
	}
	w := do("https://bank.example/token", "", nil)
	var sess string
	for _, c := range w.Result().Cookies() {
		sess = c.Value
	}
	b := w.Body.String()
	i := strings.Index(b, `"token":"`) + 9
	token := b[i : i+43]

	for origin, want := range map[string]int{
		"https://bank.example": 200,
		"http://bank.example":  403,
		"ftp://bank.example":   403,
	} {
		if got := do("https://bank.example/act", sess, map[string]string{"X-CSRF-Token": token, "Origin": origin}).Code; got != want {
			t.Errorf("TLS server, Origin %s: %d, want %d", origin, got, want)
		}
	}
	// Plain-HTTP servers (and TLS-terminating proxies) keep comparing the host
	// only: see TestOriginCheck.
}

// The form token is read with the app's own body limits, like typed routes,
// not with the package variables.
func TestFormTokenUsesTheAppsLimits(t *testing.T) {
	app := aemeath.New()
	app.SetMaxBodyBytes(100)
	app.SetMaxMultipartBytes(400)
	app.Use(session.Middleware(session.Config{Store: session.NewMemoryStore(), Insecure: true}), csrf.Middleware(csrf.Config{}))
	aemeath.GET(app, "/token", func(c *aemeath.Ctx, _ none) (tok, error) { return tok{csrf.Token(c)}, nil })
	// A plain route: only csrf reads the body, so only its limit applies.
	app.Handle("POST", "/plain", func(c *aemeath.Ctx) error { return c.JSON(200, "ok") })
	sess, token := start(app)

	post := func(contentType, body string) int {
		r := httptest.NewRequest("POST", "/plain", strings.NewReader(body))
		r.Host = "app.test"
		r.Header.Set("Content-Type", contentType)
		r.AddCookie(&http.Cookie{Name: "aemeath_session", Value: sess})
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w.Code
	}

	if code := post("application/x-www-form-urlencoded", "csrf_token="+token); code != 200 {
		t.Fatalf("small urlencoded form: %d, want 200", code)
	}
	if code := post("application/x-www-form-urlencoded", "csrf_token="+token+"&pad="+strings.Repeat("p", 200)); code != 413 {
		t.Errorf("urlencoded form over the app's 100-byte limit: %d, want 413", code)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("csrf_token", token)
	_ = mw.WriteField("pad", strings.Repeat("p", 600))
	_ = mw.Close()
	if code := post(mw.FormDataContentType(), buf.String()); code != 413 {
		t.Errorf("multipart form over the app's 400-byte limit: %d, want 413", code)
	}
}
