package aemeath_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/naufalraism/aemeath"
)

type limitJSONIn struct {
	Name string `json:"name"`
}

type limitFormIn struct {
	Title string `form:"title"`
}

type limitItem struct {
	X string `json:"x" validate:"required"`
}

type limitManyIn struct {
	Items []limitItem `json:"items"`
}

func limitsApp(setup func(*aemeath.App)) *aemeath.App {
	app := aemeath.New()
	if setup != nil {
		setup(app)
	}
	ok := func(*aemeath.Ctx, limitJSONIn) (string, error) { return "ok", nil }
	aemeath.POST(app, "/json", ok)
	aemeath.POST(app, "/form", func(*aemeath.Ctx, limitFormIn) (string, error) { return "ok", nil })
	aemeath.POST(app, "/many", func(*aemeath.Ctx, limitManyIn) (string, error) { return "ok", nil })
	app.Handle("POST", "/raw", func(c *aemeath.Ctx) error {
		if _, err := io.ReadAll(c.R.Body); err != nil {
			return err // a *http.MaxBytesError becomes 413
		}
		return c.JSON(200, "ok")
	})
	return app
}

type limitResult struct {
	json, form, multipart, raw int
	validationErrors           int
}

func probeLimits(t *testing.T, app *aemeath.App) limitResult {
	t.Helper()
	send := func(path, ct, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w
	}
	var mp bytes.Buffer
	mw := multipart.NewWriter(&mp)
	_ = mw.WriteField("title", strings.Repeat("m", 1000))
	_ = mw.Close()

	var res limitResult
	res.json = send("/json", "application/json", `{"name":"`+strings.Repeat("j", 100)+`"}`).Code
	res.form = send("/form", "application/x-www-form-urlencoded", "title="+strings.Repeat("f", 100)).Code
	res.multipart = send("/form", mw.FormDataContentType(), mp.String()).Code
	res.raw = send("/raw", "application/octet-stream", strings.Repeat("r", 200)).Code
	w := send("/many", "application/json", `{"items":[{},{},{},{},{}]}`)
	var body struct {
		Errors []json.RawMessage `json:"errors"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	res.validationErrors = len(body.Errors)
	return res
}

var (
	smallLimits = limitResult{json: 413, form: 413, multipart: 413, raw: 413, validationErrors: 2}
	largeLimits = limitResult{json: 200, form: 200, multipart: 200, raw: 200, validationErrors: 5}
)

// Each app has its own limits. They used to be package variables, so two
// apps in one process (two parallel tests, say) could not differ, and one
// configuring itself while the other served was a data race.
func TestEachAppHasItsOwnLimits(t *testing.T) {
	small := limitsApp(func(a *aemeath.App) {
		a.SetMaxBodyBytes(64)
		a.SetMaxMultipartBytes(512)
		a.SetMaxRequestBytes(128)
		a.SetMaxValidationErrors(2)
	})

	var wg sync.WaitGroup
	var large *aemeath.App
	wg.Add(2)
	go func() { // small serves while large is configured
		defer wg.Done()
		for range 20 {
			probeLimits(t, small)
		}
	}()
	go func() {
		defer wg.Done()
		large = limitsApp(func(a *aemeath.App) {
			a.SetMaxBodyBytes(1 << 20)
			a.SetMaxMultipartBytes(1 << 20)
			a.SetMaxRequestBytes(1 << 20)
			a.SetMaxValidationErrors(0) // no limit
		})
	}()
	wg.Wait()

	if got := probeLimits(t, small); got != smallLimits {
		t.Errorf("small app: got %+v, want %+v", got, smallLimits)
	}
	if got := probeLimits(t, large); got != largeLimits {
		t.Errorf("large app: got %+v, want %+v", got, largeLimits)
	}
}

// An app that sets nothing still follows the deprecated package variables,
// even when they change after New, so existing code behaves as before.
func TestAppLimitsDefaultToThePackageVariables(t *testing.T) {
	app := limitsApp(nil)
	if got := probeLimits(t, app); got != (limitResult{200, 200, 200, 200, 5}) {
		t.Fatalf("defaults: got %+v", got)
	}
	saved := []int64{aemeath.DefaultMaxBodyBytes, aemeath.DefaultMaxMultipartBytes, aemeath.DefaultMaxRequestBytes}
	savedV := aemeath.MaxValidationErrors
	t.Cleanup(func() {
		aemeath.DefaultMaxBodyBytes, aemeath.DefaultMaxMultipartBytes, aemeath.DefaultMaxRequestBytes = saved[0], saved[1], saved[2]
		aemeath.MaxValidationErrors = savedV
	})
	aemeath.DefaultMaxBodyBytes = 64
	aemeath.DefaultMaxMultipartBytes = 512
	aemeath.DefaultMaxRequestBytes = 128
	aemeath.MaxValidationErrors = 2
	if got := probeLimits(t, app); got != smallLimits {
		t.Errorf("after changing the package variables: got %+v, want %+v", got, smallLimits)
	}
}

// A setter overrides only its own limit; the others still follow the
// package variables.
func TestOneSetterLeavesTheOtherLimitsAlone(t *testing.T) {
	app := limitsApp(func(a *aemeath.App) { a.SetMaxBodyBytes(64) })
	got := probeLimits(t, app)
	want := limitResult{json: 413, form: 413, multipart: 200, raw: 200, validationErrors: 5}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	// The request cap (0 = the larger of the two) follows the app's limits.
	app2 := limitsApp(func(a *aemeath.App) { a.SetMaxBodyBytes(100); a.SetMaxMultipartBytes(150) })
	r := httptest.NewRequest("POST", "/raw", strings.NewReader(strings.Repeat("r", 200)))
	w := httptest.NewRecorder()
	app2.ServeHTTP(w, r)
	if w.Code != 413 {
		t.Errorf("request cap from the app's limits: %d, want 413", w.Code)
	}
}

// Limits must be set before the first request, like Use.
func TestLimitSettersPanicOnceServing(t *testing.T) {
	app := limitsApp(nil)
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	for name, set := range map[string]func(){
		"SetMaxBodyBytes":        func() { app.SetMaxBodyBytes(1) },
		"SetMaxMultipartBytes":   func() { app.SetMaxMultipartBytes(1) },
		"SetMaxRequestBytes":     func() { app.SetMaxRequestBytes(1) },
		"SetMaxValidationErrors": func() { app.SetMaxValidationErrors(1) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s after the first request did not panic", name)
				}
			}()
			set()
		}()
	}
}
