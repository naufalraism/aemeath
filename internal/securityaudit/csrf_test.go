package securityaudit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/csrf"
	"github.com/naufalraism/aemeath/session"
)

type csrfEnv struct {
	app   *aemeath.App
	store *session.MemoryStore
}

func newCSRF() csrfEnv {
	store := session.NewMemoryStore()
	app := aemeath.New()
	app.Use(session.Middleware(session.Config{Store: store}), csrf.Middleware(csrf.Config{}))
	app.Handle("GET", "/token", func(c *aemeath.Ctx) error { return c.Text(200, csrf.Token(c)) })
	app.Handle("POST", "/login", func(c *aemeath.Ctx) error {
		s := session.From(c)
		if err := s.Renew(c.Context()); err != nil {
			return err
		}
		s.Set("user", "ann")
		return c.Text(200, "ok")
	})
	app.Handle("POST", "/transfer", func(c *aemeath.Ctx) error { return c.Text(200, "sent") })
	return csrfEnv{app, store}
}

func (e csrfEnv) do(method, path, sess string, h map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://bank.example"+path, nil)
	if sess != "" {
		r.AddCookie(&http.Cookie{Name: "aemeath_session", Value: sess})
	}
	for k, v := range h {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.app.ServeHTTP(w, r)
	return w
}

func cookieOf(w *httptest.ResponseRecorder) string {
	for _, c := range w.Result().Cookies() {
		if c.Name == "aemeath_session" {
			return c.Value
		}
	}
	return ""
}

func TestCSRF_Basics(t *testing.T) {
	e := newCSRF()
	w := e.do("GET", "/token", "", nil)
	sess, tok := cookieOf(w), w.Body.String()
	for name, tc := range map[string]struct {
		h    map[string]string
		want int
	}{
		"no token":            {nil, 403},
		"valid token":         {map[string]string{"X-CSRF-Token": tok}, 200},
		"token + evil Origin": {map[string]string{"X-CSRF-Token": tok, "Origin": "https://evil.example"}, 403},
		"token + Origin null": {map[string]string{"X-CSRF-Token": tok, "Origin": "null"}, 403},
		"lookalike origin":    {map[string]string{"X-CSRF-Token": tok, "Origin": "https://bank.example.evil.com"}, 403},
		"empty token header":  {map[string]string{"X-CSRF-Token": ""}, 403},
	} {
		got := e.do("POST", "/transfer", sess, tc.h).Code
		t.Logf("%-22s -> %d", name, got)
		if got != tc.want {
			t.Errorf("WEAKNESS: %s: got %d, want %d", name, got, tc.want)
		}
	}
}

// The Origin check compares only the host, so an http:// page on the same host
// (a plain-HTTP or MITM'd page) passes for an https:// site.
func TestCSRF_OriginSchemeIgnored(t *testing.T) {
	e := newCSRF()
	w := e.do("GET", "/token", "", nil)
	sess, tok := cookieOf(w), w.Body.String()
	got := e.do("POST", "/transfer", sess, map[string]string{"X-CSRF-Token": tok, "Origin": "http://bank.example"}).Code
	t.Logf("https site, Origin http://bank.example -> %d", got)
	if got != 403 {
		t.Errorf("WEAKNESS: Origin with a different scheme (http) was accepted on an https site (defence in depth only: the token is still required)")
	}
}

// The guide says the token changes after login. Renew keeps all session data,
// including the CSRF token, so a token learned before login stays valid.
func TestCSRF_TokenNotRotatedAtLogin(t *testing.T) {
	e := newCSRF()
	w := e.do("GET", "/token", "", nil)
	sess, before := cookieOf(w), w.Body.String()
	w = e.do("POST", "/login", sess, map[string]string{"X-CSRF-Token": before})
	newSess := cookieOf(w)
	after := e.do("GET", "/token", newSess, nil).Body.String()
	t.Logf("token before login %s..., after login %s...", before[:8], after[:8])
	if before == after {
		t.Errorf("WEAKNESS: the CSRF token is the same before and after login (docs/guide/auth.md says it changes)")
	}
	_ = strings.TrimSpace
}
