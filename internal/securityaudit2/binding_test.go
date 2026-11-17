package securityaudit2

import (
	"bytes"
	"net/http"
	"net/http/httptest"
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
