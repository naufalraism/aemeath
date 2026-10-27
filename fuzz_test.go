package aemeath_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
)

type fuzzIn struct {
	ID      int               `path:"id"`
	Page    *int              `query:"page" validate:"min=1,max=1000"`
	Tags    []string          `query:"tag" validate:"max=5"`
	Since   time.Time         `query:"since"`
	Trace   string            `header:"X-Trace" validate:"max=64"`
	Session string            `cookie:"session"`
	Name    string            `json:"name" validate:"required,min=2,max=20"`
	Email   string            `json:"email" validate:"email"`
	Role    string            `json:"role" validate:"oneof=a b c"`
	Score   float64           `json:"score" validate:"gte=0,lte=100"`
	Items   []fuzzItem        `json:"items" validate:"max=3"`
	Meta    map[string]string `json:"meta"`
	Next    *fuzzIn           `json:"next"`
}

type fuzzItem struct {
	SKU string `json:"sku" validate:"required,pattern=^[A-Z]+$"`
	Qty *int   `json:"qty" validate:"required,gt=0"`
}

// Whatever the client sends, binding and validation must answer with a
// well-formed 2xx/4xx, never panic and never 500.
func FuzzBind(f *testing.F) {
	f.Add(`{"name":"ok","items":[{"sku":"A","qty":1}]}`, "page=1&tag=a&tag=b", "trace", "s1")
	f.Add(`{}`, "", "", "")
	f.Add(`{"name":"x","email":"nope","role":"z","score":-1,"next":{"next":{"name":"y"}}}`, "page=0", "", "")
	f.Add(`[1,2,3]`, "since=yesterday&page=abc", "t", "c")
	f.Add(`{"items":[null,{"sku":null}],"meta":{"a":1}}`, "tag=%00&tag=%ff", "\x00", "\n")
	f.Add(`{"name":"`+strings.Repeat("é", 50)+`"}`, "since=2026-01-02T03:04:05Z", "", "")

	app := aemeath.New()
	app.Use(aemeath.Recover())
	aemeath.POST(app, "/x/{id}", func(_ *aemeath.Ctx, in fuzzIn) (fuzzIn, error) { return in, nil })

	f.Fuzz(func(t *testing.T, body, query, trace, session string) {
		if _, err := url.ParseQuery(query); err != nil {
			t.Skip() // the client library would not produce this request line
		}
		req, err := http.NewRequest("POST", "/x/1?"+query, strings.NewReader(body))
		if err != nil {
			t.Skip()
		}
		req.URL.RawQuery = query
		req.Header.Set("Content-Type", "application/json")
		if !strings.ContainsAny(trace, "\r\n\x00") {
			req.Header.Set("X-Trace", trace)
		}
		req.AddCookie(&http.Cookie{Name: "session", Value: "v"})
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req.WithContext(req.Context()))
		if rec.Code >= 500 {
			t.Fatalf("status %d for body=%q query=%q: %s", rec.Code, body, query, rec.Body)
		}
	})
}
