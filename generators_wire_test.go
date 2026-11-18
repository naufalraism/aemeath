package aemeath

import (
	"encoding/json"
	"strings"
	"testing"
)

type wireMeta struct {
	Note string `json:"note"`
}
type wireMoney struct{ Cents int64 }

func (wireMoney) MarshalJSON() ([]byte, error) { return []byte(`"12.50"`), nil }

type wireChild struct {
	Name string `json:"name"`
}
type wireOut struct {
	*wireMeta
	Money  wireMoney              `json:"money"`
	Raw    json.RawMessage        `json:"raw"`
	Ptrs   []*wireChild           `json:"ptrs"`
	Nested map[string][]wireChild `json:"nested"`
	Plain  []wireChild            `json:"plain"`
}

// The generators described these differently from what encoding/json sends,
// so a response failed its own Zod schema:
//   - a field promoted through a nil embedded pointer is absent;
//   - a type with MarshalJSON, and json.RawMessage, can be any JSON value;
//   - a nil pointer or nil slice inside a slice or map is null.
func TestGeneratorsFollowTheWireFormat(t *testing.T) {
	a := New()
	GET(a, "/w", func(c *Ctx, _ struct{}) (wireOut, error) { return wireOut{}, nil })

	ts, zod := a.TypeScriptClient(), a.ZodSchemas()
	spec, _ := json.Marshal(a.OpenAPI(Info{Title: "t", Version: "1"}))
	doc := string(spec)

	for _, want := range []string{
		"note?: string;",
		"money: unknown;",
		"raw: unknown;",
		"ptrs: Array<WireChild | null> | null;",
		"nested: Record<string, Array<WireChild> | null> | null;",
		"plain: Array<WireChild> | null;",
	} {
		if !strings.Contains(ts, want) {
			t.Errorf("TypeScript is missing %q", want)
		}
	}
	if strings.Contains(ts, "interface WireMoney") {
		t.Error("TypeScript still declares the Go fields of a type with MarshalJSON")
	}
	for _, want := range []string{
		"note: z.string().optional()",
		"money: z.unknown()",
		"raw: z.unknown()",
		"ptrs: z.array(z.lazy(() => WireChildSchema).nullable()).nullable()",
		"nested: z.record(z.string(), z.array(z.lazy(() => WireChildSchema)).nullable()).nullable()",
	} {
		if !strings.Contains(zod, want) {
			t.Errorf("Zod is missing %q", want)
		}
	}
	for _, want := range []string{`"money":{}`, `"raw":{}`} {
		if !strings.Contains(doc, want) {
			t.Errorf("OpenAPI is missing %s", want)
		}
	}
	if strings.Contains(doc, `"required":["note"`) || strings.Contains(doc, `,"note",`) {
		t.Error("OpenAPI lists the promoted field of a nilable embedded struct as required")
	}
}

// Go (RE2) and JavaScript regular expressions differ. `(?i)` made
// `new RegExp` throw when schemas.ts was loaded, which broke every schema in
// the file; POSIX classes and \A/\z compiled but matched something else.
func TestPatternsAreTranslatedForJavaScript(t *testing.T) {
	for src, want := range map[string]struct {
		pattern, flags string
		ok             bool
	}{
		`^[a-z]{2,3}$`:          {`^[a-z]{2,3}$`, "u", true},
		`(?i)^[a-z]+$`:          {`^[a-z]+$`, "ui", true},
		`(?is)^a.b$`:            {`^a.b$`, "uis", true},
		`(?P<year>\d{4})-\d+`:   {`(\d{4})-\d+`, "u", true}, // names are dropped: RE2 allows one twice, JavaScript does not
		`^\b+(?P<x>a)(?<x>b)$*`: {`^(?:\b)+(a)(b)(?:$)*`, "u", true},
		`\Aabc\z`:               {`^abc$`, "u", true},
		`^\d+(?:px)?$`:          {`^\d+(?:px)?$`, "u", true},
		`^[\]\[]+\\A$`:          {`^[\]\[]+\\A$`, "u", true}, // escapes are copied, "\\A" is a backslash and an A
		`^[[:alpha:]]+$`:        {`^[A-Za-z]+$`, "u", true},
		`^\p{L}+$`:              {`^\p{L}+$`, "u", true},
		`^\pL\p{Greek}\PN$`:     {`^\p{L}\p{Script=Greek}\P{N}$`, "u", true},
		`^\Qa.b\E$`:             {`^a\.b$`, "u", true},
		`^a.c$`:                 {`^a[^\n]c$`, "u", true}, // JavaScript's "." also stops at \r, U+2028, U+2029
		`^\s\S$`:                {`^[\t\n\f\r ][^\t\n\f\r ]$`, "u", true},
		`^[\s\d]$`:              {`^[\t\n\f\r \d]$`, "u", true},
		`^\#\:\_\-$`:            {`^#:_-$`, "u", true}, // u-mode refuses escaped punctuation it does not need
		`^a{,2}}]$`:             {`^a\{,2\}\}\]$`, "u", true},
		`^[]a]$`:                {`^[\]a]$`, "u", true},
		`^\101\x{1F600}\a$`:     {`^\u{41}\u{1F600}\x07$`, "u", true},
		`(?i)\bk`:               {"", "", false}, // \b differs with case folding
		`(?i)\pL`:               {"", "", false},
		`^[\S]$`:                {"", "", false},
		`^[[:^alpha:]]$`:        {"", "", false},
		`^a(?i)b$`:              {"", "", false},
		`(?U)a+`:                {"", "", false},
		`(?m)\Aabc`:             {"", "", false},
		`(?m)^a$`:               {"", "", false}, // JavaScript's ^ and $ also stop at \r
	} {
		p, f, ok := jsRegexp(src)
		if ok != want.ok || p != want.pattern || f != want.flags {
			t.Errorf("jsRegexp(%q) = %q, %q, %v; want %q, %q, %v", src, p, f, ok, want.pattern, want.flags, want.ok)
		}
	}

	type in struct {
		A string `json:"a" validate:"pattern=(?i)^[a-z]+$"`
		B string `json:"b" validate:"pattern=^[[:alpha:]]+$"`
		C string `json:"c" validate:"pattern=\\Aabc\\z"`
	}
	a := New()
	POST(a, "/p", func(c *Ctx, v in) (in, error) { return v, nil })
	zod := a.ZodSchemas()
	for _, want := range []string{
		`a: z.string().regex(new RegExp("^[a-z]+$", "ui")).nullable().optional()`,
		`b: z.string().regex(new RegExp("^[A-Za-z]+$", "u")).nullable().optional()`,
		`c: z.string().regex(new RegExp("^abc$", "u")).nullable().optional()`,
	} {
		if !strings.Contains(zod, want) {
			t.Errorf("Zod is missing %q\n%s", want, zod)
		}
	}
	if strings.Contains(zod, "(?i)") || strings.Contains(zod, "[[:alpha:]]") {
		t.Error("an untranslated Go pattern reached schemas.ts")
	}
	spec, _ := json.Marshal(a.OpenAPI(Info{Title: "t", Version: "1"}))
	if !strings.Contains(string(spec), `"pattern":"^abc$"`) || strings.Contains(string(spec), "(?i)") {
		t.Errorf("OpenAPI patterns: %s", spec)
	}
}
