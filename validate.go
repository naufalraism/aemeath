package aemeath

import (
	"fmt"
	"math"
	"net/mail"
	"net/url"
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
	if t == timeType {
		return p
	}
	for _, ff := range flatFields(t) {
		f := ff.StructField
		fp := &fieldPlan{field: f, path: ff.Path, name: fieldDisplayName(f)}
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
			if isTextType(t) {
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

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// parseRules parses the `validate` tag of a field. A rule that makes no sense
// for the field's type panics (startup), it is never ignored silently.
func parseRules(owner reflect.Type, f reflect.StructField) (required bool, rules []rule) {
	tag := f.Tag.Get("validate")
	if tag == "" || tag == "-" {
		return false, nil
	}
	class := classOf(f.Type)
	for _, part := range strings.Split(tag, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
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
			if a, err := mail.ParseAddress(s); err != nil || a.Address != s {
				return "must be a valid email address"
			}
		case "url":
			if u, err := url.Parse(s); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return "must be a valid http(s) URL"
			}
		case "uuid":
			if !uuidRe.MatchString(s) {
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

// validate checks v (a struct value) against the plan and appends every failure to errs.
func (p *typePlan) validate(v reflect.Value, prefix string, errs *[]FieldError) {
	for _, fp := range p.fields {
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
		absent := isAbsent(fv)
		if fp.required && absent {
			*errs = append(*errs, FieldError{Field: path, Rule: "required", Message: "is required"})
			continue
		}
		if !absent {
			if target := deref(fv); target.IsValid() {
				for _, r := range fp.rules {
					if msg := r.check(target); msg != "" {
						*errs = append(*errs, FieldError{Field: path, Rule: r.name, Message: msg})
					}
				}
			}
		}
		if fp.nested != nil {
			fp.nested.validateNested(fv, path, errs)
		}
	}
}

// validateNested descends into structs reached through pointers, slices and maps.
func (p *typePlan) validateNested(v reflect.Value, path string, errs *[]FieldError) {
	v = deref(v)
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Struct:
		p.validate(v, path, errs)
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			p.validateNested(v.Index(i), path+"["+strconv.Itoa(i)+"]", errs)
		}
	case reflect.Map:
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j]) })
		for _, k := range keys {
			p.validateNested(v.MapIndex(k), path+"["+fmt.Sprint(k)+"]", errs)
		}
	}
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
