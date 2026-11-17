package securityaudit2

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
)

type ampItem struct {
	Name string `json:"name" validate:"required"`
}

type ampIn struct {
	Items []ampItem          `json:"items"`
	ByKey map[string]ampItem `json:"by_key"`
}

func jsonPost(h http.Handler, target string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", target, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// One request under the 1 MiB body limit, made of empty objects, used to
// produce one validation error per object (349,517 errors, a 25 MB response).
func TestBinding_ValidationErrorsAreCapped(t *testing.T) {
	app := aemeath.New()
	aemeath.POST(app, "/a", func(_ *aemeath.Ctx, _ ampIn) (struct{}, error) { return struct{}{}, nil })

	var b strings.Builder
	b.WriteString(`{"items":[`)
	for b.Len() < (1<<20)-16 {
		b.WriteString(`{},`)
	}
	body := strings.TrimSuffix(b.String(), ",") + `]}`

	start := time.Now()
	rec := jsonPost(app, "/a", []byte(body))
	took := time.Since(start)
	n := strings.Count(rec.Body.String(), `"rule":"required"`)
	t.Logf("request %d bytes -> status %d, response %d bytes (%.3fx), %d errors, %v",
		len(body), rec.Code, rec.Body.Len(), float64(rec.Body.Len())/float64(len(body)), n, took)
	if rec.Code != 422 {
		t.Fatalf("status %d", rec.Code)
	}
	if n > aemeath.MaxValidationErrors || rec.Body.Len() > len(body) {
		t.Errorf("a single request makes the server build and send %d validation errors (%d bytes for a %d-byte request)", n, rec.Body.Len(), len(body))
	}
}

// Map entries used to be sorted with fmt.Sprint inside the comparison
// (187 MB allocated and 0.66 s for one 1 MiB request).
func TestBinding_MapValidationIsCheap(t *testing.T) {
	app := aemeath.New()
	aemeath.POST(app, "/a", func(_ *aemeath.Ctx, _ ampIn) (struct{}, error) { return struct{}{}, nil })

	var b strings.Builder
	b.WriteString(`{"by_key":{`)
	for i := 0; b.Len() < (1<<20)-32; i++ {
		fmt.Fprintf(&b, `"%x":{},`, i)
	}
	body := strings.TrimSuffix(b.String(), ",") + `}}`

	var ms0, ms1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms0)
	start := time.Now()
	rec := jsonPost(app, "/a", []byte(body))
	took := time.Since(start)
	runtime.ReadMemStats(&ms1)
	mb := (ms1.TotalAlloc - ms0.TotalAlloc) >> 20
	t.Logf("map with %d entries: status %d, response %d bytes, %v, %d MB allocated",
		strings.Count(body, ":{}"), rec.Code, rec.Body.Len(), took, mb)
	if rec.Body.Len() > len(body) {
		t.Errorf("response (%d bytes) larger than the request (%d bytes)", rec.Body.Len(), len(body))
	}
	if mb > 100 {
		t.Errorf("one 1 MiB request allocated %d MB", mb)
	}
}
