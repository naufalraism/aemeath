package csrf_test

import (
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
	app.Use(session.Middleware(session.Config{Store: session.NewMemoryStore(), Insecure: true}))
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
