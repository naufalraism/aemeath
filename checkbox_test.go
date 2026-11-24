package aemeath_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

type checkboxQuery struct {
	Active bool `query:"active"`
}

type checkboxForm struct {
	Email    string `form:"email"`
	Remember bool   `form:"remember"`
}

type checkboxHeader struct {
	Flag bool `header:"X-Flag"`
}

// An HTML checkbox without a value attribute submits "on" when ticked and
// nothing when not. A bool parameter refused "on" with 400, so a login form
// with "remember me" failed as soon as the box was ticked.
func TestBoolAcceptsCheckboxValues(t *testing.T) {
	app := aemeath.New()
	aemeath.GET(app, "/q", func(_ *aemeath.Ctx, in checkboxQuery) (bool, error) { return in.Active, nil })
	aemeath.POST(app, "/login", func(_ *aemeath.Ctx, in checkboxForm) (bool, error) { return in.Remember, nil })
	aemeath.GET(app, "/h", func(_ *aemeath.Ctx, in checkboxHeader) (bool, error) { return in.Flag, nil })

	do := func(r *http.Request) (int, string) {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w.Code, strings.TrimSpace(w.Body.String())
	}
	form := func(body string) *http.Request {
		r := httptest.NewRequest("POST", "/login", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}
	header := func(v string) *http.Request {
		r := httptest.NewRequest("GET", "/h", nil)
		r.Header.Set("X-Flag", v)
		return r
	}

	for _, tc := range []struct {
		name string
		req  *http.Request
		code int
		body string
	}{
		{"query on", httptest.NewRequest("GET", "/q?active=on", nil), 200, "true"},
		{"query off", httptest.NewRequest("GET", "/q?active=off", nil), 200, "false"},
		{"query ON", httptest.NewRequest("GET", "/q?active=ON", nil), 200, "true"},
		{"form ticked checkbox", form("email=a%40b.c&remember=on"), 200, "true"},
		{"form unticked checkbox (field absent)", form("email=a%40b.c"), 200, "false"},
		{"header on", header("on"), 200, "true"},
		// What ParseBool accepted before still works.
		{"query true", httptest.NewRequest("GET", "/q?active=true", nil), 200, "true"},
		{"query 0", httptest.NewRequest("GET", "/q?active=0", nil), 200, "false"},
		// Anything else is still refused.
		{"query yes", httptest.NewRequest("GET", "/q?active=yes", nil), 400, ""},
		{"query onn", httptest.NewRequest("GET", "/q?active=onn", nil), 400, ""},
		{"form garbage", form("remember=maybe"), 400, ""},
	} {
		code, body := do(tc.req)
		if code != tc.code || (tc.body != "" && body != tc.body) {
			t.Errorf("%s: %d %s, want %d %s", tc.name, code, body, tc.code, tc.body)
		}
	}
}
