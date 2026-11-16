package aemeath_test

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

type sentChild struct {
	Email string `json:"email" validate:"email"`
}
type sentIn struct {
	Name     string               `json:"name" validate:"required"`
	Website  string               `json:"website" validate:"url"`
	Tags     []string             `json:"tags" validate:"min=1"`
	Kind     int                  `json:"kind" validate:"oneof=1 2"`
	Ratio    float64              `json:"ratio" validate:"gt=0"`
	Age      *int                 `json:"age" validate:"gte=18"`
	Children []sentChild          `json:"children"`
	ByKey    map[string]sentChild `json:"by_key"`
	Q        string               `query:"q" validate:"min=3"`
}

// Rules used to be skipped for every empty value, so {"website": ""} and
// {"tags": []} were accepted by the server while the generated Zod schema
// rejected them. In JSON, a key that is present is now validated; a missing
// key or null is still "not provided".
func TestJSONKeysThatArePresentAreValidated(t *testing.T) {
	app := aemeath.New()
	aemeath.POST(app, "/x", func(c *aemeath.Ctx, in sentIn) (map[string]bool, error) { return map[string]bool{"ok": true}, nil })
	post := func(target, body string) (int, string) {
		r := httptest.NewRequest("POST", target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	for _, tc := range []struct {
		name, target, body string
		want               int
		field              string
	}{
		{"only the required field", "/x", `{"name":"a"}`, 200, ""},
		{"null means not provided", "/x", `{"name":"a","website":null,"tags":null,"age":null,"children":null}`, 200, ""},
		{"valid values", "/x", `{"name":"a","website":"https://a.co","tags":["x"],"kind":1,"ratio":0.5,"age":20}`, 200, ""},
		{"empty string is validated", "/x", `{"name":"a","website":""}`, 422, `"field":"website"`},
		{"empty array is validated", "/x", `{"name":"a","tags":[]}`, 422, `"field":"tags"`},
		{"zero is validated (oneof)", "/x", `{"name":"a","kind":0}`, 422, `"field":"kind"`},
		{"zero is validated (gt)", "/x", `{"name":"a","ratio":0}`, 422, `"field":"ratio"`},
		{"pointer to zero is validated", "/x", `{"name":"a","age":0}`, 422, `"field":"age"`},
		{"nested: missing key", "/x", `{"name":"a","children":[{}]}`, 200, ""},
		{"nested: empty string", "/x", `{"name":"a","children":[{"email":"a@b.co"},{"email":""}]}`, 422, `"field":"children[1].email"`},
		{"map: empty string", "/x", `{"name":"a","by_key":{"k":{"email":""}}}`, 422, `"field":"by_key[k].email"`},
		{"required still fails on empty", "/x", `{"name":""}`, 422, `"rule":"required"`},
		{"a key in another case still binds and validates", "/x", `{"name":"a","WEBSITE":"nope"}`, 422, `"field":"website"`},
		{"empty query value is not sent", "/x?q=", `{"name":"a"}`, 200, ""},
		{"query value is validated", "/x?q=ab", `{"name":"a"}`, 422, `"field":"q"`},
	} {
		code, body := post(tc.target, tc.body)
		if code != tc.want || !strings.Contains(body, tc.field) {
			t.Errorf("%s: %d %s; want %d containing %q", tc.name, code, strings.TrimSpace(body), tc.want, tc.field)
		}
	}
}

// An HTML form always submits its empty inputs, so in a form an empty value
// still means "not provided".
func TestEmptyFormValuesAreNotProvided(t *testing.T) {
	type in struct {
		Name    string `form:"name" validate:"required"`
		Website string `form:"website" validate:"url"`
	}
	app := aemeath.New()
	aemeath.POST(app, "/f", func(c *aemeath.Ctx, v in) (map[string]bool, error) { return map[string]bool{"ok": true}, nil })
	post := func(v url.Values) int {
		r := httptest.NewRequest("POST", "/f", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w.Code
	}
	if got := post(url.Values{"name": {"a"}, "website": {""}}); got != 200 {
		t.Errorf("empty form input: %d, want 200", got)
	}
	if got := post(url.Values{"name": {"a"}, "website": {"nope"}}); got != 422 {
		t.Errorf("invalid form input: %d, want 422", got)
	}
}

// The schemas say the same: a request may send null for an optional slice,
// map or pointer (a response can contain it, so it must round-trip).
func TestGeneratedInputAcceptsNullWhereTheServerDoes(t *testing.T) {
	app := aemeath.New()
	aemeath.POST(app, "/x", func(c *aemeath.Ctx, in sentIn) (map[string]bool, error) { return nil, nil })
	zod := app.ZodSchemas()
	for _, want := range []string{
		"tags: z.array(z.string()).min(1).nullable().optional()",
		"age: z.number().int().gte(18).nullable().optional()",
		`website: z.string().regex(new RegExp("^[hH][tT][tT][pP][sS]?://`,
	} {
		if !strings.Contains(zod, want) {
			t.Errorf("Zod is missing %q", want)
		}
	}
	if ts := app.TypeScriptClient(); !strings.Contains(ts, "tags?: Array<string> | null") {
		t.Error("TypeScript: tags should accept null in the request")
	}
}
