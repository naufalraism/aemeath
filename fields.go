package aemeath

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
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

// jsRegexp translates a Go (RE2) regular expression into a JavaScript one
// that accepts exactly the same strings, for the Zod schemas and OpenAPI.
// flags always has "u": without it JavaScript works on UTF-16 code units,
// so "." or "[^a]" does not match one emoji and a range such as [😀-😂] is
// a SyntaxError that stops the whole schemas file from loading. Where the
// two engines differ, the pattern is rewritten:
//
//   - "." (without (?s)) is [^\n]: JavaScript's "." also stops at \r,
//     U+2028 and U+2029.
//   - \s is [\t\n\f\r ]: JavaScript's also takes \v, U+00A0, U+3000...
//   - \pL, \p{Greek}: \p{L}, \p{Script=Greek}; [[:alpha:]]: [A-Za-z].
//   - octal \101, \x{1F600}, \a: \u{...}; \Q...\E: escaped literals.
//   - \# \: \_ and other escaped punctuation, a literal "{" "}" "]" (as in
//     "a{,2}"): what u-mode JavaScript accepts for the same characters.
//
// ok is false when no faithful translation is known: (?m) (JavaScript's ^
// and $ also stop at \r), flags in the middle of the pattern, \b, \B or
// \p with (?i), \S or a negated POSIX class inside brackets, \C. The
// server still enforces the pattern then.
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
	if strings.Contains(flags, "m") {
		return "", "", false
	}
	ci, dotAll := strings.Contains(flags, "i"), strings.Contains(flags, "s")
	var b strings.Builder
	inClass, classStart := false, false
	for i := 0; i < len(src); {
		c := src[i]
		if c == '\\' {
			if i+1 >= len(src) {
				return "", "", false
			}
			if !jsEscape(&b, src, &i, inClass, ci) {
				return "", "", false
			}
			classStart = false
			continue
		}
		if inClass {
			switch {
			case c == ']' && classStart:
				b.WriteString(`\]`) // RE2: a "]" first in a class is a literal
				i++
			case c == ']':
				b.WriteByte(']')
				inClass = false
				i++
			case c == '[' && strings.HasPrefix(src[i:], "[:"):
				end := strings.Index(src[i:], ":]")
				if end < 0 {
					return "", "", false
				}
				name := src[i+2 : i+end]
				ranges, known := posixClasses[name]
				if !known { // includes negated ones, [:^alpha:]
					return "", "", false
				}
				b.WriteString(ranges)
				i += end + 2
			case c == '[':
				b.WriteString(`\[`)
				i++
			default:
				_, size := utf8.DecodeRuneInString(src[i:])
				b.WriteString(src[i : i+size])
				i += size
			}
			classStart = false
			continue
		}
		switch c {
		case '[':
			inClass, classStart = true, true
			b.WriteByte('[')
			i++
			if i < len(src) && src[i] == '^' {
				b.WriteByte('^')
				i++
			}
			continue
		case '.':
			if dotAll {
				b.WriteByte('.')
			} else {
				b.WriteString(`[^\n]`)
			}
		case '{':
			if m := repetition.FindString(src[i:]); m != "" {
				b.WriteString(m)
				i += len(m)
				continue
			}
			b.WriteString(`\{`) // RE2 reads a "{" that starts no repetition as a literal
		case '}', ']':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '^', '$':
			writeAssertion(&b, string(c), src, i+1)
		case '(':
			switch {
			case strings.HasPrefix(src[i:], "(?P<"), strings.HasPrefix(src[i:], "(?<"):
				// A plain group: RE2 allows a name twice, JavaScript does
				// not, and a test for a match needs no names.
				end := strings.IndexByte(src[i:], '>')
				if end < 0 {
					return "", "", false
				}
				b.WriteByte('(')
				i += end + 1
				continue
			case strings.HasPrefix(src[i:], "(?:"), !strings.HasPrefix(src[i:], "(?"):
				b.WriteByte('(')
			default:
				return "", "", false // flags in the middle: JavaScript has no equivalent everywhere
			}
		default:
			_, size := utf8.DecodeRuneInString(src[i:])
			b.WriteString(src[i : i+size])
			i += size
			continue
		}
		i++
	}
	return b.String(), "u" + strings.ReplaceAll(flags, "m", ""), true
}

// writeAssertion writes an empty-width assertion (^ $ \\b \\B). RE2 lets a
// quantifier follow one ("\\b+", "^*"); u-mode JavaScript does not, so then
// it goes in a group, which means the same.
func writeAssertion(b *strings.Builder, a, src string, next int) {
	if next < len(src) && (strings.IndexByte("*+?", src[next]) >= 0 || repetition.MatchString(src[next:])) {
		b.WriteString("(?:" + a + ")")
		return
	}
	b.WriteString(a)
}

// repetition is RE2's {n}, {n,} or {n,m}; any other "{" is a literal.
var repetition = regexp.MustCompile(`^\{[0-9]+(,[0-9]*)?\}`)

// jsSyntax are the characters u-mode JavaScript lets you escape (plus "-"
// in a class); escaping any other punctuation is a SyntaxError there.
const jsSyntax = `^$\.*+?()[]{}|/`

// posixClasses are RE2's ASCII classes, written for inside JavaScript brackets.
var posixClasses = map[string]string{
	"alnum": `0-9A-Za-z`, "alpha": `A-Za-z`, "ascii": `\x00-\x7f`, "blank": `\t `,
	"cntrl": `\x00-\x1f\x7f`, "digit": `0-9`, "graph": `!-~`, "lower": `a-z`,
	"print": ` -~`, "punct": "!-\\/:-@\\[-`{-~", "space": `\t\n\v\f\r `, "upper": `A-Z`,
	"word": `0-9A-Za-z_`, "xdigit": `0-9A-Fa-f`,
}

// jsEscape writes the JavaScript form of the escape at src[*i] ("\\" and
// what follows) and moves *i past it.
func jsEscape(b *strings.Builder, src string, i *int, inClass, ci bool) bool {
	n := src[*i+1]
	start := *i
	*i += 2
	switch {
	case n == 'A' || n == 'z':
		if inClass {
			return false
		}
		if n == 'A' {
			writeAssertion(b, "^", src, *i)
		} else {
			writeAssertion(b, "$", src, *i)
		}
	case n == 'b' || n == 'B':
		if inClass || ci { // with i and u, JavaScript counts ſ and K (Kelvin) as word characters
			return false
		}
		writeAssertion(b, src[start:*i], src, *i)
	case n == 'd' || n == 'D' || n == 'w' || n == 'W' || n == 'f' || n == 't' || n == 'n' || n == 'r' || n == 'v':
		b.WriteString(src[start:*i])
	case n == 's':
		if inClass {
			b.WriteString(`\t\n\f\r `)
		} else {
			b.WriteString(`[\t\n\f\r ]`)
		}
	case n == 'S':
		if inClass {
			return false
		}
		b.WriteString(`[^\t\n\f\r ]`)
	case n == 'a':
		b.WriteString(`\x07`)
	case n == 'p' || n == 'P':
		if ci {
			return false // case folding of properties is not the same in both engines
		}
		name := ""
		if *i < len(src) && src[*i] == '{' {
			end := strings.IndexByte(src[*i:], '}')
			if end < 0 {
				return false
			}
			name = src[*i+1 : *i+end]
			*i += end + 1
		} else if *i < len(src) {
			name = src[*i : *i+1]
			*i++
		}
		neg := n == 'P'
		if strings.HasPrefix(name, "^") {
			neg, name = !neg, name[1:]
		}
		var prop string
		switch {
		case name == "Any":
			prop = "Any"
		case unicode.Categories[name] != nil:
			prop = name
		case unicode.Scripts[name] != nil:
			prop = "Script=" + name
		default:
			return false
		}
		if neg {
			b.WriteString(`\P{` + prop + `}`)
		} else {
			b.WriteString(`\p{` + prop + `}`)
		}
	case n == 'x':
		if *i < len(src) && src[*i] == '{' {
			end := strings.IndexByte(src[*i:], '}')
			if end < 0 {
				return false
			}
			b.WriteString(`\u{` + src[*i+1:*i+end] + `}`)
			*i += end + 1
		} else if *i+2 <= len(src) {
			b.WriteString(src[start : *i+2])
			*i += 2
		} else {
			return false
		}
	case n >= '0' && n <= '7':
		end := *i
		for end < len(src) && end < *i+2 && src[end] >= '0' && src[end] <= '7' {
			end++
		}
		v, err := strconv.ParseUint(src[*i-1:end], 8, 32)
		if err != nil {
			return false
		}
		b.WriteString(`\u{` + strconv.FormatUint(v, 16) + `}`)
		*i = end
	case n == 'Q':
		lit := src[*i:]
		end := strings.Index(lit, `\E`)
		if end >= 0 {
			lit = lit[:end]
			*i += end + 2
		} else {
			*i = len(src)
		}
		for _, r := range lit {
			if strings.ContainsRune(jsSyntax, r) || (inClass && r == '-') {
				b.WriteByte('\\')
			}
			b.WriteRune(r)
		}
	case n < utf8.RuneSelf && !unicode.IsLetter(rune(n)) && !unicode.IsDigit(rune(n)):
		// Escaped punctuation: keep the backslash only where JavaScript allows it.
		if strings.IndexByte(jsSyntax, n) >= 0 || (inClass && n == '-') {
			b.WriteString(src[start:*i])
		} else {
			b.WriteByte(n)
		}
	default:
		return false // \C and anything RE2 itself would refuse
	}
	return true
}

// isTextType reports whether encoding/json serializes t as a JSON string
// through MarshalText (time.Time, UUID types, net.IP, ...).
func isTextType(t reflect.Type) bool {
	return t == timeType || t.Implements(textMarshalerType) || reflect.PointerTo(t).Implements(textMarshalerType)
}
