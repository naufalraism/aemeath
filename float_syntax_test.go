package aemeath_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/naufalraism/aemeath"
)

// strconv.ParseFloat reads Go source syntax, so "1_000" and "0x1p4" passed
// as floats while the same spellings were refused for int fields and in
// JSON bodies. A float parameter is now a plain decimal number, as a URL
// or a form would carry it.
func TestFloatParametersAreDecimal(t *testing.T) {
	type In struct {
		F float64 `query:"f"`
		G float32 `query:"g"`
	}
	app := aemeath.New()
	aemeath.GET(app, "/", func(c *aemeath.Ctx, in In) (In, error) { return in, nil })
	code := func(key, v string) int {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", "/?"+key+"="+url.QueryEscape(v), nil))
		return w.Code
	}
	for _, key := range []string{"f", "g"} {
		for _, good := range []string{"5", "+5", "-5", ".5", "5.", "-0", "007", "1e3", "1E-3", "1.5e+2"} {
			if c := code(key, good); c != http.StatusOK {
				t.Errorf("%s=%s -> %d, want 200", key, good, c)
			}
		}
		for _, bad := range []string{"1_0", "1_000.5", "0x1p4", "0X1P-2", "0x10", "0b1", "0o7", "1e", "--5", ""} {
			if bad == "" {
				continue // an empty value is "not sent"
			}
			if c := code(key, bad); c != http.StatusBadRequest {
				t.Errorf("%s=%s -> %d, want 400", key, bad, c)
			}
		}
	}
}
