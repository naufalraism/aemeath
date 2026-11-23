package aemeath_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

// NaN fails no comparison (NaN < 0 and NaN > 100 are both false), so
// "?price=NaN" passed validate:"gte=0,lte=100" and reached the handler, and a
// response echoing it failed with 500. Infinities passed any rule without an
// upper or lower bound. A request parameter is now refused (400) when it is
// not a finite number, and the numeric rules refuse NaN (422) wherever it
// comes from.
func TestNonFiniteFloatsAreRefused(t *testing.T) {
	type Params struct {
		ID     float64 `path:"id"`
		Price  float64 `query:"price"`
		Ranged float64 `query:"ranged" validate:"gte=0,lte=100"`
		H      float64 `header:"X-Weight"`
		C      float64 `cookie:"w"`
	}
	type Body struct {
		Price float64 `json:"price,string" validate:"gte=0,lte=100"`
	}
	app := aemeath.New()
	aemeath.GET(app, "/p/{id}", func(c *aemeath.Ctx, in Params) (Params, error) { return in, nil })
	aemeath.POST(app, "/b", func(c *aemeath.Ctx, in Body) (Body, error) { return in, nil })

	for _, bad := range []string{"NaN", "nan", "Inf", "-Inf", "infinity", "-infinity"} {
		for _, req := range []*http.Request{
			httptest.NewRequest("GET", "/p/"+bad, nil),
			httptest.NewRequest("GET", "/p/1?price="+bad, nil),
			httptest.NewRequest("GET", "/p/1?ranged="+bad, nil),
			func() *http.Request {
				r := httptest.NewRequest("GET", "/p/1", nil)
				r.Header.Set("X-Weight", bad)
				return r
			}(),
			func() *http.Request {
				r := httptest.NewRequest("GET", "/p/1", nil)
				r.AddCookie(&http.Cookie{Name: "w", Value: bad})
				return r
			}(),
		} {
			w := httptest.NewRecorder()
			app.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("%s %s (X-Weight %q, Cookie %q) -> %d %s, want 400", req.Method, req.URL, req.Header.Get("X-Weight"), req.Header.Get("Cookie"), w.Code, w.Body.String())
			}
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("POST", "/b", strings.NewReader(`{"price":"`+bad+`"}`)))
		if w.Code != http.StatusBadRequest && w.Code != http.StatusUnprocessableEntity {
			t.Errorf(`POST /b {"price":%q} -> %d %s, want 400 or 422`, bad, w.Code, w.Body.String())
		}
	}

	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/p/1.5?price=2&ranged=50", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("finite values -> %d %s, want 200", w.Code, w.Body.String())
	}
}
