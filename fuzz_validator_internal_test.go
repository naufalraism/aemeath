package aemeath

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

var fuzzKinds = []reflect.Type{
	reflect.TypeFor[string](), reflect.TypeFor[*string](), reflect.TypeFor[int](), reflect.TypeFor[*int](),
	reflect.TypeFor[int8](), reflect.TypeFor[uint16](), reflect.TypeFor[uint64](), reflect.TypeFor[float32](),
	reflect.TypeFor[float64](), reflect.TypeFor[bool](), reflect.TypeFor[[]string](), reflect.TypeFor[[]int](),
	reflect.TypeFor[map[string]int](), reflect.TypeFor[[2]int](), reflect.TypeFor[time.Time](),
	reflect.TypeFor[*time.Time](), reflect.TypeFor[json.Number](), reflect.TypeFor[[]byte](),
	reflect.TypeFor[any](), reflect.TypeFor[time.Duration](),
}

// Whatever validate tag a developer writes on whatever field type, the
// framework must either refuse it at startup with a clear "aemeath:" message,
// or accept it and then never panic or answer 500, whatever the client sends;
// and the generators must not panic on it.
func FuzzValidatorTags(f *testing.F) {
	for i, tag := range []string{
		"required", "min=1", "max=3", "len=2", "gt=0", "gte=-1", "lt=10", "lte=1e3", "oneof=a b",
		"email", "url", "uuid", "pattern=^a+$", "required,min=2,max=1", "min=", "len=-1", "oneof=",
		"pattern=(", "pattern=a,b", "max=99999999999999999999", "gt=NaN", "min=0x10", "lte=1e400",
		"oneof=1 2", "min=1,required", ",,", "unknownrule", "required,omitempty", "oneof=a  b",
		"pattern=", "gte=+Inf", "len=1.5", "min=-0", "oneof=\"a\" b", "max=-1",
	} {
		f.Add(uint8(i), tag, []byte(`"abc"`))
		f.Add(uint8(i*7), tag, []byte(`[1,2]`))
		f.Add(uint8(i*3), tag, []byte(`null`))
	}
	f.Fuzz(func(t *testing.T, kind uint8, tag string, value []byte) {
		typ := reflect.StructOf([]reflect.StructField{{
			Name: "V", Type: fuzzKinds[int(kind)%len(fuzzKinds)],
			Tag: reflect.StructTag(`json:"v" validate:` + strconv.Quote(tag)),
		}})
		app := New()
		var perr any
		func() {
			defer func() { perr = recover() }()
			registerRuntimeRoute(app, "POST", "/v", typ, typ)
		}()
		if perr != nil {
			if msg, ok := perr.(string); ok && strings.HasPrefix(msg, "aemeath:") {
				return // refused at startup, with a message: fine
			}
			t.Fatalf("type %s, tag %q: startup panicked without a clear message: %v", typ.Field(0).Type, tag, perr)
		}
		for name, gen := range map[string]func(){
			"OpenAPI":    func() { app.OpenAPI(Info{Title: "t", Version: "1"}) },
			"TypeScript": func() { _ = app.TypeScriptClient() },
			"Zod":        func() { _ = app.ZodSchemas() },
		} {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("type %s, tag %q: %s generator panicked: %v", typ.Field(0).Type, tag, name, r)
					}
				}()
				gen()
			}()
		}
		body := `{"v":` + string(value) + `}`
		if !json.Valid([]byte(body)) {
			b, _ := json.Marshal(string(value))
			body = `{"v":` + string(b) + `}`
		}
		rec := func() (rec fmt.Stringer) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("type %s, tag %q, body %s: the request panicked: %v", typ.Field(0).Type, tag, body, r)
				}
			}()
			r := doJSON(app, "POST", "/v", body)
			if r.Code != 200 && r.Code != 400 && r.Code != 422 {
				t.Fatalf("type %s, tag %q, body %s: status %d %s", typ.Field(0).Type, tag, body, r.Code, r.Body.String())
			}
			return r.Body
		}()
		_ = rec
	})
}
