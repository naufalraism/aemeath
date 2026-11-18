package aemeath

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

type EmbA struct {
	Name string `json:"name"`
	X    int    `json:"x"`
}
type EmbB struct {
	X int `json:"x"`
	Y int `json:"y"`
}
type embLower struct {
	Promoted string `json:"promoted"`
}
type EmbText string

type embOuterWins struct {
	EmbA
	Name string `json:"name"` // shallower: wins over EmbA.Name
}
type embTaggedWins struct {
	EmbA
	Other struct {
		X int `json:"x"`
	} `json:"other"`
}
type embTaggedVsUntagged struct {
	A struct{} `json:"-"`
	EmbA
	EmbC
}
type EmbC struct {
	X int // untagged "X": not the same name as "x" (keys are case-sensitive on the wire)
}
type embNamedByTag struct {
	EmbA `json:"inner"` // a tagged embedded struct is a field, not flattened
}
type embPointer struct {
	*EmbB
	Z int `json:"z"`
}
type embUnexported struct {
	embLower // unexported embedded struct: its exported fields are promoted
}
type embNonStruct struct {
	EmbText // embedded non-struct: a field named "EmbText"
}

// `json:",omitempty"` gives no name: encoding/json still flattens EmbA.
type embOmitEmptyTag struct {
	EmbA `json:",omitempty"`
	Z    int `json:"z"`
}

// `json:"-"` on an embedded struct leaves all of it out of JSON; its path
// parameter is still bound.
type EmbIDParam struct {
	ID   int    `path:"id"`
	Note string `json:"note"`
}
type embSkipped struct {
	EmbIDParam `json:"-"`
	Z          int `json:"z"`
}

type EmbD struct {
	V int `json:"v"`
}
type EmbE struct {
	W int `json:"v"`
}

// Types go vet would flag (two fields with one json name), built at run time.
var (
	// EmbA.x and EmbB.x at the same depth: encoding/json drops x.
	embTwoSameDepth = reflect.StructOf([]reflect.StructField{
		{Name: "EmbA", Type: reflect.TypeFor[EmbA](), Anonymous: true},
		{Name: "EmbB", Type: reflect.TypeFor[EmbB](), Anonymous: true},
	})
	// EmbD.v and EmbE.v, both tagged, same depth: dropped.
	embBothTagged = reflect.StructOf([]reflect.StructField{
		{Name: "EmbD", Type: reflect.TypeFor[EmbD](), Anonymous: true},
		{Name: "EmbE", Type: reflect.TypeFor[EmbE](), Anonymous: true},
	})
)

func TestEmbeddedKeysMatchTheWire(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[embOuterWins](),
		embTwoSameDepth,
		reflect.TypeFor[embTaggedWins](),
		reflect.TypeFor[embTaggedVsUntagged](),
		reflect.TypeFor[embNamedByTag](),
		reflect.TypeFor[embPointer](),
		reflect.TypeFor[embUnexported](),
		reflect.TypeFor[embNonStruct](),
		reflect.TypeFor[embOmitEmptyTag](),
		reflect.TypeFor[embSkipped](),
		embBothTagged,
	} {
		want := wireKeysFilled(t, typ)
		name := typ.Name()
		if name == "" {
			name = typ.String()[:min(24, len(typ.String()))]
		}
		app := New()
		var perr any
		func() {
			defer func() { perr = recover() }()
			registerRuntimeRoute(app, "GET", "/r", reflect.TypeFor[struct{}](), typ)
			registerRuntimeRoute(app, "POST", "/w", typ, reflect.TypeFor[struct{}]())
		}()
		if perr != nil {
			t.Logf("%-24s refused at startup: %v", name, perr)
			continue
		}
		doc := app.OpenAPI(Info{Title: "t", Version: "1"})
		out := schemaKeys(doc, dig(doc, "paths", "/r", "get", "responses", "200", "content", "application/json", "schema"))
		in := schemaKeys(doc, dig(doc, "paths", "/w", "post", "requestBody", "content", "application/json", "schema"))
		status := "ok"
		if strings.Join(out, ",") != strings.Join(want, ",") || strings.Join(in, ",") != strings.Join(want, ",") {
			status = "DIFFERENT"
			t.Errorf("%s: encoding/json keys %q, OpenAPI output %q, input %q", name, want, out, in)
		}
		t.Logf("%-24s wire %-28q openapi out %-28q in %-28q %s", name, want, out, in, status)
	}
}

// wireKeysFilled marshals a value with every reachable field set, so that
// nil embedded pointers and omitempty hide nothing.
func wireKeysFilled(t *testing.T, typ reflect.Type) []string {
	v := reflect.New(typ).Elem()
	fill(v)
	m := map[string]any{}
	b, err := jsonMarshalForTest(v.Interface())
	if err != nil {
		t.Fatal(err)
	}
	if err := jsonUnmarshalForTest(b, &m); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func fill(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() && v.CanSet() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		fill(v.Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if f := v.Field(i); f.CanSet() || v.Type().Field(i).Anonymous {
				if f.CanSet() {
					fill(f)
				} else if f.Kind() == reflect.Struct {
					fillUnexported(f)
				}
			}
		}
	case reflect.Int:
		v.SetInt(1)
	case reflect.String:
		v.SetString("x")
	}
}

func fillUnexported(v reflect.Value) {
	for i := 0; i < v.NumField(); i++ {
		if f := v.Field(i); f.CanSet() {
			fill(f)
		}
	}
}

// badTagIn has a json name the classic encoding/json refuses ("price€", so
// it uses "Price") but encoding/json on v2 (Go 1.27's default) accepts, and
// a field named "-". Built at run time: staticcheck flags the first tag,
// which is the point.
var badTagIn = reflect.StructOf([]reflect.StructField{
	{Name: "Price", Type: reflect.TypeFor[*int](), Tag: `json:"price€" validate:"required"`},
	{Name: "Dash", Type: reflect.TypeFor[int](), Tag: `json:"-,"`},
})

// End to end: the generated names are the ones the server reads.
func TestJSONNamesEndToEnd(t *testing.T) {
	app := New()
	registerRuntimeRoute(app, "POST", "/p", badTagIn, badTagIn)
	price, zodPrice := "Price", "Price: z.number()"
	if jsonV2 {
		price, zodPrice = "price€", `"price€": z.number()`
	}
	t.Logf("encoding/json on v2: %v, so the price field is %q", jsonV2, price)
	zod := app.ZodSchemas()
	for _, want := range []string{zodPrice, `"-": z.number()`} {
		if !strings.Contains(zod, want) {
			t.Errorf("schemas.ts has no %q:\n%s", want, zod)
		}
	}
	body := `{"` + price + `":5,"-":7}`
	rec := doJSON(app, "POST", "/p", body)
	t.Logf(`POST %s -> %d %s`, body, rec.Code, strings.TrimSpace(rec.Body.String()))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"`+price+`":5`) || !strings.Contains(rec.Body.String(), `"-":7`) {
		t.Error("the names in the schema are not the ones the server reads and writes")
	}
	rec = doJSON(app, "POST", "/p", `{}`)
	t.Logf("POST {} -> %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	if !strings.Contains(rec.Body.String(), `"field":"`+price+`"`) {
		t.Error("the validation error must name the key the client sends")
	}
}
