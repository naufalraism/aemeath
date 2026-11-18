package securityaudit2

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

// Vary: Origin was only sent for allowed origins, so a shared cache could
// store a response without CORS headers and serve it to the real front end.
func TestCORS_VaryOriginOnEveryResponse(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.CORS(aemeath.CORSConfig{AllowOrigins: []string{"https://app.example"}}))
	aemeath.GET(app, "/data", func(_ *aemeath.Ctx, _ struct{}) (map[string]int, error) { return map[string]int{"n": 1}, nil })
	for _, origin := range []string{"https://app.example", "https://evil.example", ""} {
		req := httptest.NewRequest("GET", "/data", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		t.Logf("Origin %-22q -> Access-Control-Allow-Origin %-22q Vary %q",
			origin, rec.Header().Get("Access-Control-Allow-Origin"), rec.Header().Values("Vary"))
		if n := strings.Count(strings.Join(rec.Header().Values("Vary"), ","), "Origin"); n != 1 {
			t.Errorf("Origin %q: Vary: Origin appears %d times, want once", origin, n)
		}
	}

	star := aemeath.New()
	star.Use(aemeath.CORS())
	aemeath.GET(star, "/data", func(_ *aemeath.Ctx, _ struct{}) (map[string]int, error) { return map[string]int{"n": 1}, nil })
	req := httptest.NewRequest("GET", "/data", nil)
	req.Header.Set("Origin", "https://anything.example")
	rec := httptest.NewRecorder()
	star.ServeHTTP(rec, req)
	t.Logf("AllowOrigins * -> Access-Control-Allow-Origin %q Vary %q", rec.Header().Get("Access-Control-Allow-Origin"), rec.Header().Values("Vary"))
	if len(rec.Header().Values("Vary")) != 0 {
		t.Error("with only *, the response is the same for every origin and needs no Vary")
	}
}

func TestCORS_MixedWildcardStillVaries(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.CORS(aemeath.CORSConfig{AllowOrigins: []string{"https://app.example", "*"}}))
	aemeath.GET(app, "/data", func(_ *aemeath.Ctx, _ struct{}) (map[string]int, error) { return map[string]int{"n": 1}, nil })
	req := httptest.NewRequest("GET", "/data", nil)
	req.Header.Set("Origin", "https://app.example")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	t.Logf("AllowOrigins [app.example *], Origin app.example -> %q Vary %q", rec.Header().Get("Access-Control-Allow-Origin"), rec.Header().Values("Vary"))
	if rec.Header().Get("Access-Control-Allow-Origin") == "https://app.example" && len(rec.Header().Values("Vary")) == 0 {
		t.Error("a response that names one origin must vary by Origin")
	}
}
