package aemeath

import (
	"strings"
	"testing"
)

type rangeIn struct {
	A int8    `json:"a"`
	B uint    `json:"b" validate:"gte=-1"`
	C float32 `json:"c"`
	D uint8   `json:"d" validate:"gt=0"`
	E int     `json:"e"`
}

// The schemas carry the range encoding/json enforces for each number type,
// and a rule never widens it (TestZodAgreesWithTheServer checks the values).
func TestNumberRangesInSchemas(t *testing.T) {
	app := New()
	registerRuntimeRoute(app, "POST", "/n", typeOf[rangeIn](), typeOf[struct{}]())
	zod := app.ZodSchemas()
	for _, want := range []string{
		"a: z.number().int().gte(-128).lte(127).optional()",
		"b: z.number().int().gte(0).gte(-1).optional()",
		"c: z.number().gte(-3.4028235677973366e+38).lte(3.4028235677973366e+38).optional()",
		"d: z.number().int().gte(0).lte(255).gt(0).optional()",
		"e: z.number().int().optional()",
	} {
		if !strings.Contains(zod, want) {
			t.Errorf("schemas.ts has no %q", want)
		}
	}
	doc := app.OpenAPI(Info{Title: "t", Version: "1"})
	props, _ := dig(doc, "paths", "/n", "post", "requestBody", "content", "application/json", "schema", "properties").(map[string]any)
	check := func(field, key string, want any) {
		t.Helper()
		got := dig(props, field, key)
		if got != want {
			t.Errorf("OpenAPI %s.%s = %v, want %v", field, key, got, want)
		}
	}
	check("a", "minimum", float64(-128))
	check("a", "maximum", float64(127))
	check("b", "minimum", float64(0)) // the gte=-1 rule does not widen it
	check("d", "exclusiveMinimum", true)
	check("e", "minimum", nil)
}
