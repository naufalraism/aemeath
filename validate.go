package aemeath

import (
	"encoding"
	"fmt"
	"math"
	"mime/multipart"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// FieldError describes one validation failure.
type FieldError struct {
	Field   string `json:"field"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// ValidationError returns a 422 error carrying per-field details. Handlers can
// return it for checks only they can make (for example "email already taken").
func ValidationError(errs ...FieldError) *HTTPError {
	return &HTTPError{Code: 422, Message: "validation failed", Errors: errs}
}

// Supported `validate` rules (comma separated, for example `validate:"required,min=3,max=50"`):
//
//	required       the value must be present and non-empty
//	min=N, max=N   string: characters; slice/map: items; number: value
//	len=N          string: characters; slice/map: items (exactly N)
//	gt, gte, lt, lte=N   number comparisons
//	oneof=a b c    one of the space separated values (string or number)
//	email, url, uuid     string formats (url must be absolute http/https)
//	pattern=REGEX  string must match the regular expression (no commas)
//
// Rules other than `required` only run when a value was provided, so an
// optional field can still be constrained. Use a pointer to make "sent as zero"
// distinguishable from "not sent".
const supportedRules = "required, min, max, len, gt, gte, lt, lte, oneof, email, url, uuid, pattern"

type rule struct {
	name string
	num  float64
	arg  string
	set  []string
	nums []float64
	re   *regexp.Regexp
}

type fieldPlan struct {
	field    reflect.StructField
	path     []int
	name     string
	jsonKey  string // key in a JSON body; "" for path, query, header, cookie and form fields
	required bool
	rules    []rule
	nested   *typePlan
}

type typePlan struct {
	fields []*fieldPlan
}

var (
	planMu sync.Mutex
	plans  = map[reflect.Type]*typePlan{}
)

// planFor builds (once) the validation plan of a struct type. It panics at
// startup for invalid rules, so a typo never becomes a silent runtime bug.
func planFor(t reflect.Type) *typePlan {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return &typePlan{}
	}
	planMu.Lock()
	defer planMu.Unlock()
	return buildPlan(t)
}

func buildPlan(t reflect.Type) *typePlan {
	if p, ok := plans[t]; ok {
		return p
	}
	p := &typePlan{}
	plans[t] = p // registered before the fields so recursive types terminate
	defer func() {
		// An invalid tag panics below. Forget the half-built plan, so a
		// recovered panic cannot leave a type that is silently not validated.
		if r := recover(); r != nil {
			delete(plans, t)
			panic(r)
		}
	}()
	if t == timeType {
		return p
	}
	for _, ff := range flatFields(t) {
		f := ff.StructField
		checkTagSyntax(t, f)
		fp := &fieldPlan{field: f, path: ff.Path, name: fieldDisplayName(f)}
		if _, param := paramSource(f); param == "" && formName(f) == "" && !jsonSkipped(f) {
			fp.jsonKey = jsonName(f)
		}
		fp.required, fp.rules = parseRules(t, f)
		if fp.required && hasOmitEmpty(f) {
			panic(fmt.Sprintf("aemeath: field %s.%s has both `required` and `omitempty`, which contradict each other", t.Name(), f.Name))
		}
		if fp.required && requiredNeedsPointer(f.Type) {
			panic(fmt.Sprintf(
				"aemeath: field %s.%s of type %s has `required` but cannot tell a missing value from a zero value; use a pointer (*%s) or remove `required`",
				t.Name(), f.Name, f.Type, f.Type))
		}
		if nt := nestedStruct(f.Type); nt != nil {
			fp.nested = buildPlan(nt)
		}
		p.fields = append(p.fields, fp)
	}
	return p
}

// nestedStruct returns the struct type reached through pointers, slices,
// arrays and maps, or nil. Types that serialize as text (time.Time, UUIDs)
// are leaves.
func nestedStruct(t reflect.Type) reflect.Type {
	for {
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			t = t.Elem()
			continue
		case reflect.Struct:
			if isTextType(t) || t == reflect.TypeFor[multipart.FileHeader]() {
				return nil
			}
			return t
		}
		return nil
	}
}

type kindClass int

const (
	kindOther kindClass = iota
	kindString
	kindNumber
	kindList
)

func classOf(t reflect.Type) kindClass {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return kindString
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return kindNumber
	case reflect.Slice, reflect.Array, reflect.Map:
		return kindList
	}
	return kindOther
}

// parseRules parses the `validate` tag of a field. A rule that makes no sense
// for the field's type panics (startup), it is never ignored silently.
// splitRules splits a `validate` tag into its rules at the commas. A regular
// expression may contain commas itself ({1,3}), so pattern= takes everything
// up to the end of the tag and must be the last rule.
func splitRules(tag string) []string {
	var parts []string
	for tag != "" {
		if rest := strings.TrimLeft(tag, " "); strings.HasPrefix(rest, "pattern=") {
			parts = append(parts, rest)
			break
		}
		part, rest, _ := strings.Cut(tag, ",")
		tag = rest
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

// ruleAfterComma finds a rule written after the pattern, as tags written
// before pattern took the rest of the tag did ("pattern=^a$,max=5").
var ruleAfterComma = regexp.MustCompile(`,\s*(required|omitempty|email|url|uuid|(min|max|len|gt|gte|lt|lte|oneof)=[^,]*)\s*$`)

func parseRules(owner reflect.Type, f reflect.StructField) (required bool, rules []rule) {
	tag := f.Tag.Get("validate")
	if tag == "" || tag == "-" {
		return false, nil
	}
	class := classOf(f.Type)
	for _, part := range splitRules(tag) {
		name, arg, hasArg := strings.Cut(part, "=")
		fail := func(msg string) {
			panic(fmt.Sprintf("aemeath: field %s.%s: validate:%q: %s", owner.Name(), f.Name, part, msg))
		}
		needArg := func() {
			if !hasArg || arg == "" {
				fail("the rule needs a value, for example " + name + "=3")
			}
		}
		noArg := func() {
			if hasArg {
				fail("the rule does not take a value")
			}
		}
		onlyFor := func(ok bool, what string) {
			if !ok {
				fail("is only supported on " + what + ", not on " + f.Type.String())
			}
		}
		r := rule{name: name, arg: arg}

		switch name {
		case "required":
			noArg()
			required = true
			continue
		case "min", "max", "len":
			needArg()
			onlyFor(class != kindOther, "strings, numbers, slices and maps")
			n, err := strconv.ParseFloat(arg, 64)
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				fail("the value must be a number")
			}
			if name == "len" {
				onlyFor(class == kindString || class == kindList, "strings, slices and maps")
			}
			if class != kindNumber && (n < 0 || n != math.Trunc(n)) {
				fail("the value must be a non-negative integer")
			}
			r.num = n
		case "gt", "gte", "lt", "lte":
			needArg()
			onlyFor(class == kindNumber, "numbers")
			n, err := strconv.ParseFloat(arg, 64)
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				fail("the value must be a number")
			}
			r.num = n
		case "oneof":
			needArg()
			onlyFor(class == kindString || class == kindNumber, "strings and numbers")
			r.set = strings.Fields(arg)
			if len(r.set) == 0 {
				fail("the rule needs at least one value")
			}
			if class == kindNumber {
				for _, s := range r.set {
					n, err := strconv.ParseFloat(s, 64)
					if err != nil {
						fail("every value must be a number, got " + strconv.Quote(s))
					}
					r.nums = append(r.nums, n)
				}
			}
		case "email", "url", "uuid":
			noArg()
			onlyFor(class == kindString, "strings")
		case "pattern":
			needArg()
			onlyFor(class == kindString, "strings")
			if m := ruleAfterComma.FindString(arg); m != "" {
				fail("pattern takes the rest of the tag, so it must be the last rule; move " + strings.TrimLeft(m, ", ") + " before it")
			}
			re, err := regexp.Compile(arg)
			if err != nil {
				fail("invalid regular expression: " + err.Error())
			}
			r.re = re
		default:
			fail("unknown rule; supported rules: " + supportedRules)
		}
		rules = append(rules, r)
	}
	return required, rules
}

// isAbsent decides whether a field counts as "not sent", based on its type.
func isAbsent(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		return v.IsNil()
	case reflect.String:
		return v.Len() == 0
	case reflect.Slice, reflect.Map:
		return v.IsNil() || v.Len() == 0
	}
	return v.IsZero()
}

func deref(v reflect.Value) reflect.Value {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	return v
}

func num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// check returns an error message, or "" when the value satisfies the rule.
func (r rule) check(v reflect.Value) string {
	switch v.Kind() {
	case reflect.String:
		s := v.String()
		n := float64(utf8.RuneCountInString(s))
		switch r.name {
		case "min":
			if n < r.num {
				return "must be at least " + num(r.num) + " characters"
			}
		case "max":
			if n > r.num {
				return "must be at most " + num(r.num) + " characters"
			}
		case "len":
			if n != r.num {
				return "must be exactly " + num(r.num) + " characters"
			}
		case "oneof":
			for _, o := range r.set {
				if s == o {
					return ""
				}
			}
			return "must be one of: " + strings.Join(r.set, ", ")
		case "email":
			if !validEmail(s) {
				return "must be a valid email address"
			}
		case "url":
			if !validURL(s) {
				return "must be a valid http(s) URL"
			}
		case "uuid":
			if !validUUID(s) {
				return "must be a valid UUID"
			}
		case "pattern":
			if !r.re.MatchString(s) {
				return "has an invalid format"
			}
		}
	case reflect.Slice, reflect.Array, reflect.Map:
		n := float64(v.Len())
		switch r.name {
		case "min":
			if n < r.num {
				return "must contain at least " + num(r.num) + " items"
			}
		case "max":
			if n > r.num {
				return "must contain at most " + num(r.num) + " items"
			}
		case "len":
			if n != r.num {
				return "must contain exactly " + num(r.num) + " items"
			}
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return r.checkNumber(float64(v.Int()))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return r.checkNumber(float64(v.Uint()))
	case reflect.Float32, reflect.Float64:
		return r.checkNumber(v.Float())
	}
	return ""
}

func (r rule) checkNumber(x float64) string {
	// NaN compares false with everything, so without this it would pass
	// min, max, gt and lt (a float read with json:",string" can be "NaN").
	if math.IsNaN(x) && r.name != "oneof" {
		return "must be a number"
	}
	switch r.name {
	case "min", "gte":
		if x < r.num {
			return "must be at least " + num(r.num)
		}
	case "max", "lte":
		if x > r.num {
			return "must be at most " + num(r.num)
		}
	case "gt":
		if x <= r.num {
			return "must be greater than " + num(r.num)
		}
	case "lt":
		if x >= r.num {
			return "must be less than " + num(r.num)
		}
	case "oneof":
		for _, o := range r.nums {
			if x == o {
				return ""
			}
		}
		return "must be one of: " + strings.Join(r.set, ", ")
	}
	return ""
}

// validate checks v (a struct value) against the plan and appends every
// failure to errs.
//
// sent is the decoded JSON body at this level (a map[string]any), or nil when
// the value did not come from JSON (forms, query strings). Rules run when a
// value was provided:
//
//   - in JSON, a key that is present with a non-null value is provided, even
//     when it is empty: {"website": ""} fails `url`, {"tags": []} fails
//     `min=1`. A missing key or null is not provided.
//   - elsewhere (forms, query, headers), an empty value counts as not sent,
//     because an HTML form always submits its empty inputs.
//
// Decoding the body a second time to learn its keys costs as much as the first
// decode, so the caller may pass sent as nil together with a non-nil needSent.
// Validation then runs without that knowledge and sets *needSent as soon as a
// rule is skipped only because the keys are unknown; most bodies never reach
// that point, and the caller decodes and validates again only when it happens.
// In the second run needSent is nil, so there is no third run.
func (p *typePlan) validate(v reflect.Value, prefix string, sent any, errs *[]FieldError, limit int, needSent *bool) {
	keys, _ := sent.(map[string]any)
	for _, fp := range p.fields {
		if errorsFull(*errs, limit) {
			return // the validation error limit is reached: the rest is not looked at
		}
		path := fp.name
		if prefix != "" {
			path = prefix + "." + fp.name
		}
		fv, ok := fieldByPath(v, fp.path, false)
		if !ok { // a nil embedded pointer on the way: the field was not sent
			if fp.required {
				*errs = append(*errs, FieldError{Field: path, Rule: "required", Message: "is required"})
			}
			continue
		}
		var sentValue any
		if keys != nil && fp.jsonKey != "" {
			sentValue = keys[fp.jsonKey]
		}
		if needSent != nil && *needSent {
			return // the caller is going to decode the body and start over
		}
		empty := isAbsent(fv)
		if fp.required && empty {
			*errs = append(*errs, FieldError{Field: path, Rule: "required", Message: "is required"})
			continue
		}
		if !empty || sentValue != nil {
			if target := deref(fv); target.IsValid() {
				for _, r := range fp.rules {
					if msg := r.check(target); msg != "" {
						*errs = append(*errs, FieldError{Field: path, Rule: r.name, Message: msg})
					}
				}
			}
		} else if needSent != nil && len(fp.rules) > 0 && fp.jsonKey != "" {
			// The value is empty: whether these rules apply depends on whether
			// the key was sent, which this run does not know.
			*needSent = true
			return
		}
		if fp.nested != nil {
			fp.nested.validateNested(fv, path, sentValue, errs, limit, needSent)
			if needSent != nil && *needSent {
				return
			}
		}
	}
}

// validateNested descends into structs reached through pointers, slices and
// maps, keeping the matching part of the JSON body alongside.
func (p *typePlan) validateNested(v reflect.Value, path string, sent any, errs *[]FieldError, limit int, needSent *bool) {
	v = deref(v)
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Struct:
		p.validate(v, path, sent, errs, limit, needSent)
	case reflect.Slice, reflect.Array:
		items, _ := sent.([]any)
		for i := 0; i < v.Len(); i++ {
			var item any
			if i < len(items) {
				item = items[i]
			}
			p.validateNested(v.Index(i), path+"["+strconv.Itoa(i)+"]", item, errs, limit, needSent)
			if (needSent != nil && *needSent) || errorsFull(*errs, limit) {
				return
			}
		}
	case reflect.Map:
		entries, _ := sent.(map[string]any)
		// Each key is turned into its text once, then sorted by that text, so
		// errors come in a stable order. (Formatting inside the comparison cost
		// two fmt.Sprint calls per comparison: 118 MB for a 1 MiB body.)
		type entry struct {
			name string
			key  reflect.Value
		}
		list := make([]entry, 0, v.Len())
		for it := v.MapRange(); it.Next(); {
			list = append(list, entry{mapKeyName(it.Key()), it.Key()})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].name < list[j].name })
		for _, e := range list {
			p.validateNested(v.MapIndex(e.key), path+"["+e.name+"]", entries[e.name], errs, limit, needSent)
			if (needSent != nil && *needSent) || errorsFull(*errs, limit) {
				return
			}
		}
	}
}

// mapKeyName is a map key as encoding/json writes it: string kinds as they
// are, then encoding.TextMarshaler, then integers in decimal.
func mapKeyName(k reflect.Value) string {
	if k.Kind() == reflect.String {
		return k.String()
	}
	if tm, ok := k.Interface().(encoding.TextMarshaler); ok {
		if b, err := tm.MarshalText(); err == nil {
			return string(b)
		}
	}
	switch k.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(k.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(k.Uint(), 10)
	}
	return fmt.Sprint(k.Interface())
}

// hasRules reports whether the plan, or a plan nested in it, has a rule that
// depends on whether a value was sent. Only then is the body decoded a second
// time to learn which keys it has.
func (p *typePlan) hasRules(seen map[*typePlan]bool) bool {
	if p == nil || seen[p] {
		return false
	}
	seen[p] = true
	for _, fp := range p.fields {
		if len(fp.rules) > 0 || fp.nested.hasRules(seen) {
			return true
		}
	}
	return false
}

// requiredNeedsPointer reports whether a non-pointer field of this type
// cannot distinguish "not sent" from "sent as the zero value".
func requiredNeedsPointer(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.String, reflect.Slice, reflect.Map, reflect.Pointer, reflect.Interface:
		return false
	}
	return true
}

// inputRequired reports whether a client must send the field. That is the
// case for `validate:"required"`, and for a non-pointer struct that contains a
// required field somewhere inside (the server validates it even when omitted).
func inputRequired(f reflect.StructField) bool {
	if hasRequired(f) {
		return true
	}
	t := f.Type
	if t.Kind() != reflect.Struct || isTextType(t) {
		return false
	}
	planMu.Lock()
	defer planMu.Unlock()
	return planHasRequired(buildPlan(t), map[*typePlan]bool{})
}

func planHasRequired(p *typePlan, seen map[*typePlan]bool) bool {
	if p == nil || seen[p] {
		return false
	}
	seen[p] = true
	for _, fp := range p.fields {
		if fp.required {
			return true
		}
		// A nested struct reached through a pointer, slice or map is optional.
		if fp.field.Type.Kind() == reflect.Struct && !isTextType(fp.field.Type) && planHasRequired(fp.nested, seen) {
			return true
		}
	}
	return false
}
