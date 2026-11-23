package securityaudit

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/tracing"
)

func TestDocs_ThirdPartyScriptWithoutIntegrity(t *testing.T) {
	app := aemeath.New()
	app.EnableDocs(aemeath.Info{Title: "t", Version: "1"})
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/docs", nil))
	tag := regexp.MustCompile(`<script[^>]+src="https?://[^"]+"[^>]*>`).FindString(w.Body.String())
	t.Logf("/docs -> %d, external script: %s", w.Code, tag)
	if tag != "" && !strings.Contains(tag, "integrity=") {
		t.Error("WEAKNESS: /docs loads a CDN script without Subresource Integrity: a compromised CDN runs code on your origin")
	}
}

func TestCORS_Origins(t *testing.T) {
	app := aemeath.New()
	app.Use(aemeath.CORS(aemeath.CORSConfig{AllowOrigins: []string{"https://app.example"}, AllowCredentials: true}))
	app.Handle("GET", "/me", func(c *aemeath.Ctx) error { return c.Text(200, "secret") })
	for _, o := range []string{"https://app.example", "https://app.example.evil.com", "https://evilapp.example", "null", "http://app.example"} {
		r := httptest.NewRequest("GET", "/me", nil)
		r.Header.Set("Origin", o)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		acao := w.Header().Get("Access-Control-Allow-Origin")
		t.Logf("Origin %-32q -> ACAO %q", o, acao)
		if o != "https://app.example" && acao != "" {
			t.Errorf("WEAKNESS: origin %q is allowed", o)
		}
	}
	func() {
		defer func() { t.Logf("credentials + \"*\" -> panic: %v", recover() != nil) }()
		aemeath.CORS(aemeath.CORSConfig{AllowOrigins: []string{"*"}, AllowCredentials: true})
	}()
}

// The tracer honours the sampled flag of an incoming traceparent, so any
// client can force 100% sampling even with SampleRatio 0.
func TestTracing_ClientForcesSampling(t *testing.T) {
	tr := tracing.New(tracing.Config{ServiceName: "x", Endpoint: "http://127.0.0.1:1/v1/traces", SampleRatio: 0.0000001})
	app := aemeath.New()
	app.Use(tr.Middleware())
	sampled := 0
	app.Handle("GET", "/", func(c *aemeath.Ctx) error {
		if s := tracing.FromContext(c.Context()); s != nil && s.Sampled() {
			sampled++
		}
		return nil
	})
	for i := 0; i < 100; i++ {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
		app.ServeHTTP(httptest.NewRecorder(), r)
	}
	t.Logf("SampleRatio ~0, 100 requests with traceparent ...-01 from the client: %d sampled", sampled)
	if sampled == 100 {
		t.Log("NOTE: external clients decide sampling (W3C default); costs and queue drops are in their hands")
	}
}
