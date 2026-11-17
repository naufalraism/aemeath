package aemeath

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
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
	return dominantFields(flattenFields(t, nil, 0, false, false))
}

// jsonSkipTag marks a field promoted from an embedded struct tagged
// `json:"-"`: encoding/json leaves the whole embedded struct out, but its
// path, query, header, cookie and form fields are still bound.
const jsonSkipTag = ` aemeath_json:"skip"`

// dominantFields applies encoding/json's rule for fields that share a JSON
// name: the shallowest one wins; at the same depth, the one with a json tag
// wins; two at the same depth that are both tagged, or both untagged, cancel
// out and neither is sent or read. jsonNameConflict reports the last case,
// which registration refuses.
func dominantFields(all []flatField) []flatField {
	drop := map[int]bool{}
	for _, group := range jsonNameGroups(all) {
		if len(group) < 2 {
			continue
		}
		if tie(all, group) {
			for _, i := range group {
				drop[i] = true
			}
			continue
		}
		for _, i := range group[1:] {
			drop[i] = true
		}
	}
	if len(drop) == 0 {
		return all
	}
	out := make([]flatField, 0, len(all)-len(drop))
	for i, f := range all {
		if !drop[i] {
			out = append(out, f)
		}
	}
	return out
}

// jsonNameGroups returns, for each JSON name used by more than zero fields,
// the indexes of those fields in all, sorted as encoding/json sorts them:
// shallowest first, then tagged before untagged, then in declaration order.
func jsonNameGroups(all []flatField) [][]int {
	byName := map[string][]int{}
	var names []string
	for i, f := range all {
		if jsonSkipped(f.StructField) || !f.IsExported() {
			continue
		}
		n := jsonName(f.StructField)
		if _, ok := byName[n]; !ok {
			names = append(names, n)
		}
		byName[n] = append(byName[n], i)
	}
	groups := make([][]int, 0, len(names))
	for _, n := range names {
		g := byName[n]
		sort.SliceStable(g, func(a, b int) bool {
			fa, fb := all[g[a]], all[g[b]]
			if len(fa.Path) != len(fb.Path) {
				return len(fa.Path) < len(fb.Path)
			}
			return hasJSONName(fa.StructField) && !hasJSONName(fb.StructField)
		})
		groups = append(groups, g)
	}
	return groups
}

func tie(all []flatField, group []int) bool {
	a, b := all[group[0]], all[group[1]]
	return len(a.Path) == len(b.Path) && hasJSONName(a.StructField) == hasJSONName(b.StructField)
}

// jsonNameConflict reports two fields of t that cancel each other out
// (see dominantFields), named by their path ("Address.City").
func jsonNameConflict(t reflect.Type) (first, second, name string, found bool) {
	all := flattenFields(t, nil, 0, false, false)
	for _, group := range jsonNameGroups(all) {
		if len(group) > 1 && tie(all, group) {
			return fieldPathName(t, all[group[0]].Path), fieldPathName(t, all[group[1]].Path), jsonName(all[group[0]].StructField), true
		}
	}
	return "", "", "", false
}

func fieldPathName(t reflect.Type, path []int) string {
	var parts []string
	for _, i := range path {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		f := t.Field(i)
		parts = append(parts, f.Name)
		t = f.Type
	}
	return strings.Join(parts, ".")
}

// checkJSONNames refuses, at registration, a type that encoding/json cannot
// send whole: two fields with the same JSON name at the same depth (neither
// travels). It looks through pointers, slices, arrays, maps and fields.
func checkJSONNames(t reflect.Type, seen map[reflect.Type]bool) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || seen[t] || t == timeType || isCustomJSON(t) {
		return
	}
	seen[t] = true
	if a, b, name, ok := jsonNameConflict(t); ok {
		panic(fmt.Sprintf("aemeath: %s: fields %s and %s both have the JSON name %q at the same depth, so encoding/json sends and reads neither; rename one, or give the one that should win a json tag", t, a, b, name))
	}
	for _, f := range flatFields(t) {
		checkJSONNames(f.Type, seen)
	}
}

// viaPointerTag marks a field promoted through an embedded pointer. When that
// pointer is nil, encoding/json leaves the field out, so in responses it is
// optional even without omitempty.
const viaPointerTag = ` aemeath_via:"pointer"`

// promotedViaPointer reports whether f reached the top level through an
// embedded pointer (see viaPointerTag).
func promotedViaPointer(f reflect.StructField) bool {
	return f.Tag.Get("aemeath_via") == "pointer"
}

// outputOptional reports whether a response may leave the field out.
func outputOptional(f reflect.StructField) bool {
	return hasOmitEmpty(f) || promotedViaPointer(f)
}

func flattenFields(t reflect.Type, prefix []int, depth int, viaPtr, skipped bool) []flatField {
	var out []flatField
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		path := append(append([]int(nil), prefix...), i)
		if f.Anonymous && depth < maxSchemaDepth {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			// encoding/json flattens an embedded struct unless its tag names
			// it (`json:",omitempty"` names nothing), and leaves it out
			// entirely with `json:"-"`.
			if ft.Kind() == reflect.Struct && !hasJSONName(f) {
				out = append(out, flattenFields(ft, path, depth+1, viaPtr || f.Type.Kind() == reflect.Pointer, skipped || f.Tag.Get("json") == "-")...)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if viaPtr {
			f.Tag += viaPointerTag
		}
		if skipped {
			f.Tag += jsonSkipTag
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

var (
	textMarshalerType   = reflect.TypeFor[encoding.TextMarshaler]()
	jsonMarshalerType   = reflect.TypeFor[json.Marshaler]()
	jsonUnmarshalerType = reflect.TypeFor[json.Unmarshaler]()
)

// isCustomJSON reports whether t writes or reads its own JSON (MarshalJSON or
// UnmarshalJSON, which includes json.RawMessage). Its Go fields say nothing
// about what travels on the wire, so the generators describe it as unknown.
// time.Time is excluded: its JSON form is known.
func isCustomJSON(t reflect.Type) bool {
	if t == timeType {
		return false
	}
	p := reflect.PointerTo(t)
	return t.Implements(jsonMarshalerType) || p.Implements(jsonMarshalerType) || p.Implements(jsonUnmarshalerType)
}

// elemNullable reports whether an element of a slice, array or map can be
// JSON null in a response: a nil pointer, slice, map or interface.
func elemNullable(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map:
		return true
	}
	return false
}

// jsRegexp translates a Go (RE2) regular expression into one JavaScript
// understands, for the Zod schemas and the OpenAPI document. It handles
// leading flags ((?i), (?s), (?m)), named groups ((?P<n>...)) and the \A / \z
// anchors. ok is false when the pattern uses something JavaScript reads
// differently or rejects (POSIX classes, \Q...\E, \p{...}, flags in the
// middle of the pattern); the server still enforces the pattern then.
func jsRegexp(src string) (pattern, flags string, ok bool) {
	for strings.HasPrefix(src, "(?") {
		end := strings.IndexByte(src, ')')
		if end < 0 {
			break
		}
		group := src[2:end]
		if group == "" || strings.Trim(group, "ims") != "" {
			break
		}
		for _, f := range group {
			if !strings.ContainsRune(flags, f) {
				flags += string(f)
			}
		}
		src = src[end+1:]
	}
	var b strings.Builder
	inClass := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '\\' && i+1 < len(src):
			n := src[i+1]
			switch {
			case n == 'A' && !inClass:
				if strings.Contains(flags, "m") {
					return "", "", false
				}
				b.WriteByte('^')
			case n == 'z' && !inClass:
				if strings.Contains(flags, "m") {
					return "", "", false
				}
				b.WriteByte('$')
			case n == 'Q', n == 'E', n == 'C', n == 'p', n == 'P', n == 'a', n == 'x' && i+2 < len(src) && src[i+2] == '{':
				return "", "", false
			default:
				b.WriteByte(c)
				b.WriteByte(n)
			}
			i++
		case c == '[' && !inClass:
			inClass = true
			b.WriteByte(c)
		case c == '[' && inClass && i+1 < len(src) && src[i+1] == ':':
			return "", "", false // POSIX class such as [[:alpha:]]
		case c == ']' && inClass:
			inClass = false
			b.WriteByte(c)
		case c == '(' && !inClass && strings.HasPrefix(src[i:], "(?P<"):
			b.WriteString("(?<")
			i += 3
		case c == '(' && !inClass && strings.HasPrefix(src[i:], "(?") && !strings.HasPrefix(src[i:], "(?:"):
			return "", "", false // flags or groups JavaScript does not have
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), flags, true
}

// isTextType reports whether encoding/json serializes t as a JSON string
// through MarshalText (time.Time, UUID types, net.IP, ...).
func isTextType(t reflect.Type) bool {
	return t == timeType || t.Implements(textMarshalerType) || reflect.PointerTo(t).Implements(textMarshalerType)
}
