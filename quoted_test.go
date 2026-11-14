package aemeath

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

type quotedIn struct {
	ID    int64    `json:"id,string" validate:"min=1"`
	Ratio *float64 `json:"ratio,string,omitempty"`
	On    bool     `json:"on,string"`
	Kind  int      `json:"kind,string" validate:"oneof=1 2"`
	Plain int      `json:"plain"`
}

// encoding/json sends `,string` numbers and booleans as JSON strings. The
// generators used to describe them as numbers, so the generated client sent
// 5 where the server required "5" (400), and parsed "5" with z.number().
func TestQuotedFieldsAreStringsEverywhere(t *testing.T) {
	a := New()
	POST(a, "/q", func(c *Ctx, in quotedIn) (quotedIn, error) { return in, nil })

	ts := a.TypeScriptClient()
	for _, want := range []string{"id: string", "on: string", "plain: number", `kind?: "1" | "2"`} {
		if !strings.Contains(ts, want) {
			t.Errorf("TypeScript is missing %q", want)
		}
	}
	if !strings.Contains(ts, "ratio?: string | null") {
		t.Errorf("TypeScript: ratio should be an optional nullable string")
	}

	z := a.ZodSchemas()
	for _, want := range []string{`id: z.string().regex(/^-?\d+$/)`, `on: z.string().regex(/^(true|false)$/)`, `plain: z.number().int()`} {
		if !strings.Contains(z, want) {
			t.Errorf("Zod is missing %q", want)
		}
	}
	if strings.Contains(z, "id: z.string().regex(/^-?\\d+$/).min(") {
		t.Error("Zod must not turn the numeric min into a string length")
	}

	spec, _ := json.Marshal(a.OpenAPI(Info{Title: "t", Version: "1"}))
	if !strings.Contains(string(spec), `"id":{"pattern":"^-?\\d+$","type":"string"}`) {
		t.Errorf("OpenAPI: id should be a string with a digits pattern")
	}

	// What the generated types describe is what the server accepts and sends.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/q", strings.NewReader(`{"id":"5","on":"true","kind":"2","plain":1}`))
	r.Header.Set("Content-Type", "application/json")
	a.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"5"`) {
		t.Fatalf("round trip: %d %s", w.Code, w.Body)
	}
	// And the server still enforces the numeric rule.
	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/q", strings.NewReader(`{"id":"-3","on":"true","kind":"2","plain":1}`))
	r.Header.Set("Content-Type", "application/json")
	a.ServeHTTP(w, r)
	if w.Code != 422 {
		t.Fatalf("min=1 on a quoted number: %d %s", w.Code, w.Body)
	}
}
