package aemeath_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

// The tag was split at every comma, so a pattern such as ^\d{1,3}$ panicked
// at startup with `validate:"3}$": unknown rule`.
func TestPatternMayContainCommas(t *testing.T) {
	type in struct {
		Code string `json:"code" validate:"required,pattern=^\\d{1,3}$"`
		Tag  string `json:"tag" validate:"max=5, pattern=^(a|b,c)$"`
	}
	app := aemeath.New()
	aemeath.POST(app, "/x", func(c *aemeath.Ctx, v in) (in, error) { return v, nil })
	post := func(body string) int {
		r := httptest.NewRequest("POST", "/x", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w.Code
	}
	for body, want := range map[string]int{
		`{"code":"123","tag":"b,c"}`: 200,
		`{"code":"1234","tag":"a"}`:  422, // 4 digits
		`{"code":"12","tag":"b"}`:    422, // tag matches neither alternative
		`{"tag":"a"}`:                422, // required still applies before the pattern
		`{"code":"7","tag":"a"}`:     200,
	} {
		if got := post(body); got != want {
			t.Errorf("%s: %d, want %d", body, got, want)
		}
	}
	// The pattern reaches the generated schemas whole.
	if z := app.ZodSchemas(); !strings.Contains(z, `{1,3}`) {
		t.Errorf("Zod lost the pattern:\n%s", z)
	}
}

// A rule written after the pattern would silently become part of the regular
// expression; refuse it with a message that says what to do.
func TestRuleAfterPatternPanics(t *testing.T) {
	type in struct {
		Code string `json:"code" validate:"pattern=^[a-z]+$,max=5"`
	}
	defer func() {
		msg, _ := recover().(string)
		if !strings.Contains(msg, "must be the last rule") || !strings.Contains(msg, "max=5") {
			t.Fatalf("panic = %q", msg)
		}
	}()
	aemeath.POST(aemeath.New(), "/x", func(c *aemeath.Ctx, v in) (in, error) { return v, nil })
}
