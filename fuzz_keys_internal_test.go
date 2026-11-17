package aemeath

import (
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// wireKeys is what encoding/json actually sends for a value of type t.
func wireKeys(t *testing.T, typ reflect.Type) []string {
	t.Helper()
	v := reflect.New(typ).Elem()
	for i := 0; i < v.NumField(); i++ { // non-zero values, so omitempty keeps every key
		switch f := v.Field(i); f.Kind() {
		case reflect.Int:
			f.SetInt(1)
		case reflect.String:
			f.SetString("x")
		}
	}
	b, err := json.Marshal(v.Interface())
	if err != nil {
		t.Skip() // not encodable: not this test's business
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// schemaKeys collects the property names of an OpenAPI schema, following $ref.
func schemaKeys(doc map[string]any, schema any) []string {
	s, _ := schema.(map[string]any)
	if ref, ok := s["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/components/schemas/")
		comps, _ := doc["components"].(map[string]any)
		schemas, _ := comps["schemas"].(map[string]any)
		s, _ = schemas[name].(map[string]any)
	}
	props, _ := s["properties"].(map[string]any)
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func dig(m any, path ...string) any {
	for _, p := range path {
		mm, ok := m.(map[string]any)
		if !ok {
			return nil
		}
		m = mm[p]
	}
	return m
}

// propRe finds property names in generated TypeScript: an identifier or a
// string literal, followed by an optional "?" and ":".
var propRe = regexp.MustCompile(`(?m)(?:^|[{;,(]|\n)\s*("(?:[^"\\\n]|\\.)*"|[A-Za-z_$][A-Za-z0-9_$]*)\??\s*:`)

func tsKeys(src string) map[string]bool {
	keys := map[string]bool{}
	for _, m := range propRe.FindAllStringSubmatch(src, -1) {
		k := m[1]
		if strings.HasPrefix(k, `"`) {
			d, err := decodeJSString(k)
			if err != nil {
				keys["<invalid literal "+k+">"] = true
				continue
			}
			k = d
		}
		keys[k] = true
	}
	return keys
}

func structWith(tagA, tagB string) reflect.Type {
	return reflect.StructOf([]reflect.StructField{
		{Name: "Alpha", Type: reflect.TypeFor[int](), Tag: reflect.StructTag(`json:` + strconv.Quote(tagA))},
		{Name: "Bravo", Type: reflect.TypeFor[string](), Tag: reflect.StructTag(`json:` + strconv.Quote(tagB))},
	})
}

// The key names in OpenAPI, the TypeScript client and the Zod schemas must be
// the keys encoding/json puts on the wire, for any json tag.
func FuzzGeneratedKeysMatchTheWire(f *testing.F) {
	for _, s := range [][2]string{
		{"alpha", "bravo"}, {"", ""}, {"-", "b"}, {"-,", "b"}, {"a,omitempty", "b,string"},
		{"same", "same"}, {`we"ird`, `back\slash`}, {"price€", "naïve"}, {"a b", "a-b"},
		{"\a", "x"}, {"\U000e0001", "y"}, {"__proto__", "constructor"}, {"0lead", "$ok"},
	} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, tagA, tagB string) {
		if strings.ContainsAny(tagA+tagB, "\x00") {
			t.Skip()
		}
		typ := structWith(tagA, tagB)
		want := wireKeys(t, typ)

		app := New()
		var registerErr any
		func() {
			defer func() { registerErr = recover() }()
			registerRuntimeRoute(app, "GET", "/r", reflect.TypeFor[struct{}](), typ)
			registerRuntimeRoute(app, "POST", "/w", typ, reflect.TypeFor[struct{}]())
		}()
		if registerErr != nil {
			if msg, _ := registerErr.(string); strings.HasPrefix(msg, "aemeath:") {
				t.Skip() // refused at startup with a clear message: fine
			}
			t.Fatalf("tags %q %q: registering the route panicked: %v", tagA, tagB, registerErr)
		}

		doc := app.OpenAPI(Info{Title: "t", Version: "1"})
		got := schemaKeys(doc, dig(doc, "paths", "/r", "get", "responses", "200", "content", "application/json", "schema"))
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("tags %q %q: encoding/json sends keys %q, OpenAPI documents %q", tagA, tagB, want, got)
		}

		gotIn := schemaKeys(doc, dig(doc, "paths", "/w", "post", "requestBody", "content", "application/json", "schema"))
		if strings.Join(gotIn, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("tags %q %q: encoding/json reads keys %q, OpenAPI documents the request body as %q", tagA, tagB, want, gotIn)
		}
		zod := tsKeys(app.ZodSchemas())
		for _, k := range want {
			if !zod[k] {
				t.Errorf("tags %q %q: key %q is read from the wire but not in the Zod schema", tagA, tagB, k)
			}
		}

		ts := tsKeys(app.TypeScriptClient())
		for _, k := range want {
			if !ts[k] {
				t.Errorf("tags %q %q: key %q is on the wire but not in the TypeScript client", tagA, tagB, k)
			}
		}
		for _, goName := range []string{"Alpha", "Bravo"} {
			if ts[goName] && !contains(want, goName) {
				t.Errorf("tags %q %q: the TypeScript client has %q, which is not on the wire %q", tagA, tagB, goName, want)
			}
		}
		for k := range ts {
			if strings.HasPrefix(k, "<invalid literal") {
				t.Errorf("tags %q %q: %s", tagA, tagB, k)
			}
		}
	})
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
