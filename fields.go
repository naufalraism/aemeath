package aemeath

import (
	"encoding"
	"reflect"
)

// flatField is a struct field together with the index path that reaches it
// from the top-level struct, through any embedded structs.
type flatField struct {
	reflect.StructField
	Path []int
}

// flatFields returns the exported fields of t. Embedded structs without a
// json name are flattened, exactly like encoding/json does.
func flatFields(t reflect.Type) []flatField {
	return flattenFields(t, nil, 0)
}

func flattenFields(t reflect.Type, prefix []int, depth int) []flatField {
	var out []flatField
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		path := append(append([]int(nil), prefix...), i)
		if f.Anonymous && depth < maxSchemaDepth {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct && !hasJSONName(f) {
				out = append(out, flattenFields(ft, path, depth+1)...)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		out = append(out, flatField{f, path})
	}
	return out
}

// jsonFields returns the fields that encoding/json serializes at the top
// level of t.
func jsonFields(t reflect.Type) []reflect.StructField {
	ff := flatFields(t)
	out := make([]reflect.StructField, len(ff))
	for i, f := range ff {
		out[i] = f.StructField
	}
	return out
}

// fieldByPath walks an index path from v. When alloc is true, nil embedded
// pointers on the way are allocated; otherwise the walk reports false.
func fieldByPath(v reflect.Value, path []int, alloc bool) (reflect.Value, bool) {
	for i, idx := range path {
		if i > 0 {
			for v.Kind() == reflect.Pointer {
				if v.IsNil() {
					if !alloc {
						return reflect.Value{}, false
					}
					v.Set(reflect.New(v.Type().Elem()))
				}
				v = v.Elem()
			}
		}
		v = v.Field(idx)
	}
	return v, true
}

var textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()

// isTextType reports whether encoding/json serializes t as a JSON string
// through MarshalText (time.Time, UUID types, net.IP, ...).
func isTextType(t reflect.Type) bool {
	return t == timeType || t.Implements(textMarshalerType) || reflect.PointerTo(t).Implements(textMarshalerType)
}
