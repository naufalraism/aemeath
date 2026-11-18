package aemeath_test

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
)

type capItem struct {
	Name string `json:"name" validate:"required"`
}

type capIn struct {
	Items []capItem `json:"items"`
}

type capForm struct {
	A string `form:"a" validate:"min=3"`
	B string `form:"b" validate:"min=3"`
	C string `form:"c" validate:"min=3"`
}

func TestMaxValidationErrors(t *testing.T) {
	old := aemeath.MaxValidationErrors
	defer func() { aemeath.MaxValidationErrors = old }()

	app := aemeath.New()
	aemeath.POST(app, "/json", func(_ *aemeath.Ctx, _ capIn) (struct{}, error) { return struct{}{}, nil })
	aemeath.POST(app, "/form", func(_ *aemeath.Ctx, _ capForm) (struct{}, error) { return struct{}{}, nil })
	body := `{"items":[` + strings.TrimSuffix(strings.Repeat(`{},`, 250), ",") + `]}`

	count := func(path, contentType, body string) int {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != 422 {
			t.Fatalf("%s: status %d", path, rec.Code)
		}
		return strings.Count(rec.Body.String(), `"rule":`)
	}
	for _, tc := range []struct {
		max, json, form int
	}{
		{100, 100, 3},
		{2, 2, 2},
		{0, 250, 3}, // 0 means no limit
	} {
		aemeath.MaxValidationErrors = tc.max
		form := url.Values{"a": {"x"}, "b": {"y"}, "c": {"z"}}.Encode()
		if got := count("/json", "application/json", body); got != tc.json {
			t.Errorf("MaxValidationErrors=%d: JSON reported %d errors, want %d", tc.max, got, tc.json)
		}
		if got := count("/form", "application/x-www-form-urlencoded", form); got != tc.form {
			t.Errorf("MaxValidationErrors=%d: form reported %d errors, want %d", tc.max, got, tc.form)
		}
	}
	// The first errors are the ones reported, in order.
	aemeath.MaxValidationErrors = 3
	req := httptest.NewRequest("POST", "/json", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	for i, want := range []string{"items[0].name", "items[1].name", "items[2].name"} {
		if !strings.Contains(rec.Body.String(), `"`+want+`"`) {
			t.Errorf("error %d: %s missing in %s", i, want, rec.Body.String())
		}
	}
}
