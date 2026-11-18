package securityaudit

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

type updateIn struct {
	ID   int    `path:"id"`
	Role string `header:"X-Role"`
	Name string `json:"name" validate:"required,max=50"`
}

func bindApp() *aemeath.App {
	app := aemeath.New()
	aemeath.PUT(app, "/users/{id}", func(c *aemeath.Ctx, in updateIn) (updateIn, error) { return in, nil })
	return app
}

func put(app *aemeath.App, path, body string, h map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("PUT", path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	for k, v := range h {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}

func TestBinding_ParamsCannotBeOverriddenByBody(t *testing.T) {
	app := bindApp()
	// Field names that Go's case-insensitive JSON matching would map onto ID and Role.
	w := put(app, "/users/7", `{"name":"a","ID":999,"id":999,"Role":"admin","role":"admin"}`, nil)
	t.Logf("-> %d %s", w.Code, strings.TrimSpace(w.Body.String()))
	if strings.Contains(w.Body.String(), "999") || strings.Contains(w.Body.String(), "admin") {
		t.Error("WEAKNESS: a path/header field was set from the JSON body")
	}
}

func TestBinding_Limits(t *testing.T) {
	app := bindApp()
	big := `{"name":"` + strings.Repeat("a", 2<<20) + `"}`
	deep := strings.Repeat("[", 100000) + strings.Repeat("]", 100000)
	for name, tc := range map[string]struct {
		body string
		ct   string
		want int
	}{
		"2 MiB body":            {big, "application/json", 413},
		"100k nested arrays":    {`{"name":` + deep + `}`, "application/json", 400},
		"text/plain body":       {`{"name":"a"}`, "text/plain", 415},
		"trailing garbage":      {`{"name":"a"}{"name":"b"}`, "application/json", 400},
		"name over max":         {`{"name":"` + strings.Repeat("é", 51) + `"}`, "application/json", 422},
		"invalid UTF-8 in name": {"{\"name\":\"\xff\xfe\"}", "application/json", 200},
	} {
		r := httptest.NewRequest("PUT", "/users/1", bytes.NewBufferString(tc.body))
		r.Header.Set("Content-Type", tc.ct)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		t.Logf("%-22s -> %d", name, w.Code)
		if w.Code != tc.want {
			t.Errorf("%s: got %d, want %d (%s)", name, w.Code, tc.want, strings.TrimSpace(w.Body.String()))
		}
	}
	w := put(app, "/users/abc", `{"name":"a"}`, nil)
	t.Logf("non-numeric path id -> %d %s", w.Code, strings.TrimSpace(w.Body.String()))
	w = put(app, "/users/99999999999999999999999", `{"name":"a"}`, nil)
	t.Logf("overflowing path id -> %d %s", w.Code, strings.TrimSpace(w.Body.String()))
	if w.Code != 400 {
		t.Errorf("overflowing id: %d", w.Code)
	}
}

// Plain routes (app.Handle) read c.R.Body themselves; only typed routes get the
// 1 MiB default. Nothing limits the body unless MaxBodySize is installed.
func TestBinding_PlainRouteBodyUnlimited(t *testing.T) {
	app := aemeath.New()
	var n int
	app.Handle("POST", "/raw", func(c *aemeath.Ctx) error {
		b := new(bytes.Buffer)
		m, _ := b.ReadFrom(c.R.Body)
		n = int(m)
		return c.Text(200, "ok")
	})
	r := httptest.NewRequest("POST", "/raw", bytes.NewReader(make([]byte, 50<<20)))
	app.ServeHTTP(httptest.NewRecorder(), r)
	t.Logf("plain route read %d MiB without any limit", n>>20)
	if n == 50<<20 {
		t.Log("NOTE: plain routes have no default body limit; install aemeath.MaxBodySize")
	}
}
