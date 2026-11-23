package aemeath

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

// MaxValidationErrors is the most validation errors one request reports
// (100). Validation stops once it is reached: without a limit, a 1 MiB body of
// 349,517 empty objects made the server build and send 349,517 errors, a 25 MB
// response. Set it to 0 for no limit. Change it before the server starts.
var MaxValidationErrors = 100

// errorsFull reports whether no more validation errors should be collected.
func errorsFull(errs []FieldError) bool {
	return MaxValidationErrors > 0 && len(errs) >= MaxValidationErrors
}

// capErrors trims errs to MaxValidationErrors (a field with several failing
// rules can add more than one past the check).
func capErrors(errs []FieldError) []FieldError {
	if MaxValidationErrors > 0 && len(errs) > MaxValidationErrors {
		return errs[:MaxValidationErrors]
	}
	return errs
}

// DefaultMaxBodyBytes is the default limit for JSON request bodies (1 MiB).
// It can be changed before the server starts.
var DefaultMaxBodyBytes int64 = 1 << 20

type routeConfig struct {
	summary     string
	description string
	tags        []string
	status      int
	opID        string
	deprecated  bool
	secure      bool
}

// RouteOption configures route metadata or behavior.
type RouteOption func(*routeConfig)

// OperationID names a route explicitly (used in OpenAPI and as the TypeScript client function name).
func OperationID(id string) RouteOption { return func(c *routeConfig) { c.opID = id } }

// Summary adds a short description to the route (shown in the OpenAPI docs).
func Summary(s string) RouteOption { return func(c *routeConfig) { c.summary = s } }

// Description adds a longer description (Markdown) to the route in the OpenAPI docs.
func Description(s string) RouteOption { return func(c *routeConfig) { c.description = s } }

// Tags groups the route under one or more categories in OpenAPI.
func Tags(t ...string) RouteOption { return func(c *routeConfig) { c.tags = t } }

// Status changes the default (200 OK) HTTP status code of a successful response.
func Status(code int) RouteOption { return func(c *routeConfig) { c.status = code } }

// Deprecated marks the route as deprecated in the OpenAPI docs.
func Deprecated() RouteOption { return func(c *routeConfig) { c.deprecated = true } }

// RequireAuth makes the route answer 401 unless a middleware authenticated
// the caller (see Ctx.MarkAuthenticated; jwt.Middleware does it for you). It
// also documents the route as protected by a Bearer token in the OpenAPI
// docs, with a 401 response.
//
// It is a safety net, not the authentication itself: a route with RequireAuth
// and no authentication middleware in front of it refuses every request.
func RequireAuth() RouteOption { return func(c *routeConfig) { c.secure = true } }

func newRouteConfig(opts []RouteOption) routeConfig {
	cfg := routeConfig{status: http.StatusOK}
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// inputInfo is everything computed once, at registration, about an input type.
type inputInfo struct {
	typ    reflect.Type
	plan   *typePlan
	params []flatField // fields bound from path, query, header or cookie
	forms  []flatField // fields bound from an urlencoded or multipart form body

	sentOnce sync.Once
	wantSent bool
}

// needsSent reports whether validation needs to know which keys the JSON body
// has (see typePlan.validate). It is false for inputs without rules, which
// then pay nothing for it.
func (i *inputInfo) needsSent() bool {
	i.sentOnce.Do(func() { i.wantSent = i.plan.hasRules(map[*typePlan]bool{}) })
	return i.wantSent
}

// prepareInput analyses the input type of a route. It panics at startup for
// tag combinations that cannot work, which beats a silent bug at runtime:
//
//   - a non-pointer scalar or struct with `required` cannot tell "not sent"
//     from "sent as zero": use a pointer;
//   - `required` together with `omitempty` contradict each other;
//   - unknown or misapplied `validate` rules (see supportedRules).
//
// A json-tagged field on a bodyless method (GET/DELETE) is never bound, so a
// warning is logged.
func prepareInput(t reflect.Type, method, path string) *inputInfo {
	checkJSONNames(t, map[reflect.Type]bool{})
	info := &inputInfo{typ: t, plan: planFor(t)}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return info
	}
	for _, ff := range flatFields(t) {
		checkFormTags(t, ff.StructField)
		if src, _ := paramSource(ff.StructField); src != "" {
			info.params = append(info.params, ff)
		} else if formName(ff.StructField) != "" {
			if !bodyMethod(method) {
				panic(fmt.Sprintf("aemeath: %s %s: field %s.%s has a `form` tag but %s requests have no body", method, path, t.Name(), ff.Name, method))
			}
			info.forms = append(info.forms, ff)
		} else if !bodyMethod(method) && hasJSONName(ff.StructField) {
			log.Printf("aemeath: warning: %s %s: field %s.%s has a json tag but %s requests have no body, so it is never bound; use `query:\"...\"` or `path:\"...\"`",
				method, path, t.Name(), ff.Name, method)
		}
	}
	return info
}

// typedHandler adapts a typed handler to a HandlerFunc: it binds and
// validates the input, calls fn, and serializes the output as JSON.
// An output of type Handled means fn wrote the response itself.
func typedHandler[In, Out any](cfg routeConfig, info *inputInfo, fn func(*Ctx, In) (Out, error)) HandlerFunc {
	handled := reflect.TypeFor[Out]() == reflect.TypeFor[Handled]()
	return func(c *Ctx) error {
		if cfg.secure && !c.authed {
			return Unauthorized("authentication required")
		}
		var in In
		defer c.runCleanups()
		if err := bind(c, &in, info); err != nil {
			return err
		}
		out, err := fn(c, in)
		if err != nil {
			return err
		}
		if handled {
			return nil
		}
		return c.JSON(cfg.status, out)
	}
}

// Route registers a type-safe handler. The input (In) is bound and validated
// automatically and the output (Out) is serialized as JSON.
func Route[In, Out any](a *App, method, path string, fn func(*Ctx, In) (Out, error), opts ...RouteOption) Endpoint[In, Out] {
	cfg := newRouteConfig(opts)
	info := prepareInput(reflect.TypeFor[In](), method, path)
	checkJSONNames(reflect.TypeFor[Out](), map[reflect.Type]bool{})
	a.specs = append(a.specs, routeSpec{
		method: method, path: path,
		in: reflect.TypeFor[In](), out: reflect.TypeFor[Out](),
		cfg: cfg,
	})
	a.Handle(method, path, typedHandler(cfg, info, fn))
	return Endpoint[In, Out]{Method: method, Path: path}
}

// GET registers a type-safe HTTP GET route.
func GET[In, Out any](a *App, path string, fn func(*Ctx, In) (Out, error), o ...RouteOption) Endpoint[In, Out] {
	return Route(a, "GET", path, fn, o...)
}

// POST registers a type-safe HTTP POST route.
func POST[In, Out any](a *App, path string, fn func(*Ctx, In) (Out, error), o ...RouteOption) Endpoint[In, Out] {
	return Route(a, "POST", path, fn, o...)
}

// PUT registers a type-safe HTTP PUT route.
func PUT[In, Out any](a *App, path string, fn func(*Ctx, In) (Out, error), o ...RouteOption) Endpoint[In, Out] {
	return Route(a, "PUT", path, fn, o...)
}

// PATCH registers a type-safe HTTP PATCH route.
func PATCH[In, Out any](a *App, path string, fn func(*Ctx, In) (Out, error), o ...RouteOption) Endpoint[In, Out] {
	return Route(a, "PATCH", path, fn, o...)
}

// DELETE registers a type-safe HTTP DELETE route.
func DELETE[In, Out any](a *App, path string, fn func(*Ctx, In) (Out, error), o ...RouteOption) Endpoint[In, Out] {
	return Route(a, "DELETE", path, fn, o...)
}

// bodyMethod reports whether the HTTP method carries a request body.
func bodyMethod(m string) bool { return m == "POST" || m == "PUT" || m == "PATCH" }

// paramSources lists the struct tags that bind a field from the request line
// or headers instead of the JSON body.
var paramSources = []string{"path", "query", "header", "cookie"}

// paramSource returns ("path"|"query"|"header"|"cookie", name) if the field has such a tag.
func paramSource(f reflect.StructField) (string, string) {
	for _, src := range paramSources {
		if n := f.Tag.Get(src); n != "" {
			return src, n
		}
	}
	return "", ""
}

// jsonName returns the field name from the json tag, or the Go field name if absent.
func jsonName(f reflect.StructField) string {
	if name := jsonTagName(f); name != "" {
		return name
	}
	return f.Name
}

// jsonTagName is the name the json tag gives the field, as encoding/json reads
// it: "" when there is none or encoding/json ignores it and uses the Go field
// name. Which names it accepts depends on the Go version (tagJSONName).
// `json:"-,"` names the field "-"; only `json:"-"` leaves it out (see
// jsonSkipped).
func jsonTagName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return ""
	}
	return tagJSONName(tag)
}

// hasJSONName reports whether the json tag gives the field a name. An
// embedded struct without one is flattened into its parent.
func hasJSONName(f reflect.StructField) bool { return jsonTagName(f) != "" }

// jsonSkipped reports whether encoding/json leaves the field out: `json:"-"`
// on the field, or on an embedded struct it was promoted from.
func jsonSkipped(f reflect.StructField) bool {
	return f.Tag.Get("json") == "-" || f.Tag.Get("aemeath_json") == "skip"
}

// hasRequired reports whether the field has the `validate:"required"` tag.
func hasRequired(f reflect.StructField) bool {
	for _, p := range splitRules(f.Tag.Get("validate")) {
		if p == "required" {
			return true
		}
	}
	return false
}

// fieldDisplayName is the name shown to clients in validation errors:
// the parameter name for path/query/header/cookie fields, the JSON name otherwise.
func fieldDisplayName(f reflect.StructField) string {
	if _, name := paramSource(f); name != "" {
		return name
	}
	if name := formName(f); name != "" {
		return name
	}
	return jsonName(f)
}

// isJSONContentType reports whether the Content-Type header denotes JSON.
func isJSONContentType(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

// bind fills the struct from the JSON body and from the path, query, header
// and cookie tags, then validates it.
func bind(c *Ctx, in any, info *inputInfo) (err error) {
	if info.hasForm() {
		return bindWithForm(c, in, info)
	}
	var body []byte // the JSON body as sent, to tell "" from a missing key
	if bodyMethod(c.R.Method) && c.R.Body != nil {
		read, err := io.ReadAll(io.LimitReader(c.R.Body, DefaultMaxBodyBytes+1))
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return PayloadTooLarge(fmt.Sprintf("request body exceeds the limit of %d bytes", mbe.Limit))
			}
			return BadRequest("failed to read request body")
		}
		if int64(len(read)) > DefaultMaxBodyBytes {
			return PayloadTooLarge(fmt.Sprintf("request body exceeds the limit of %d bytes", DefaultMaxBodyBytes))
		}
		body = read
		if len(body) > 0 {
			if ct := c.R.Header.Get("Content-Type"); ct != "" && !isJSONContentType(ct) {
				return UnsupportedMediaType("Content-Type must be application/json")
			}
			if err := json.Unmarshal(body, in); err != nil {
				return BadRequest("invalid JSON body")
			}
		}
	}

	v := reflect.ValueOf(in).Elem()
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	if err := bindParams(c, v, info); err != nil {
		return err
	}
	var errs []FieldError
	var needSent bool
	var ask *bool // nil unless there is a body to decode a second time
	if len(body) > 0 && info.needsSent() {
		ask = &needSent
	}
	info.plan.validate(v, "", nil, &errs, ask)
	if needSent {
		// A rule depends on whether a key was sent: decode the body once more
		// and validate again, now knowing the keys.
		errs = nil
		info.plan.validate(v, "", decodeSent(body), &errs, nil)
	}
	if errs = capErrors(errs); len(errs) > 0 {
		return ValidationError(errs...)
	}
	return nil
}

// decodeSent decodes the body a second time, generically, so validation can
// see which keys were sent. It costs about as much as the first decode, so it
// runs only when a rule's outcome actually depends on it (see typePlan.validate).
// The body is valid JSON: it decoded once already.
func decodeSent(body []byte) any {
	var sent any
	_ = json.Unmarshal(body, &sent)
	return sent
}

// bindParams binds path, query, header and cookie values into the struct.
// Query parameters may also be slices (repeated keys: ?tag=a&tag=b).
func bindParams(c *Ctx, v reflect.Value, info *inputInfo) error {
	for _, pf := range info.params {
		src, name := paramSource(pf.StructField)
		fv, _ := fieldByPath(v, pf.Path, true)
		fv.SetZero() // a client can never set these through the JSON body

		if src == "query" && fv.Kind() == reflect.Slice && fv.Type().Elem().Kind() != reflect.Uint8 && !isTextType(fv.Type()) {
			vals := c.R.URL.Query()[name]
			if len(vals) == 0 {
				continue
			}
			sl := reflect.MakeSlice(fv.Type(), len(vals), len(vals))
			for j, raw := range vals {
				if err := setField(sl.Index(j), raw); err != nil {
					return BadRequest(fmt.Sprintf("invalid value for %s parameter '%s'", src, name))
				}
			}
			fv.Set(sl)
			continue
		}

		var raw string
		switch src {
		case "path":
			raw = c.R.PathValue(name)
		case "query":
			raw = c.R.URL.Query().Get(name)
		case "header":
			raw = c.R.Header.Get(name)
		case "cookie":
			if ck, err := c.R.Cookie(name); err == nil {
				raw = ck.Value
			}
		}
		if raw == "" {
			continue
		}
		if err := setField(fv, raw); err != nil {
			return BadRequest(fmt.Sprintf("invalid value for %s parameter '%s'", src, name))
		}
	}
	return nil
}

var textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

// setField converts a string from the request into the field's type.
// Types implementing encoding.TextUnmarshaler (time.Time, UUID types, ...) are supported.
func setField(fv reflect.Value, raw string) error {
	if fv.Kind() == reflect.Pointer {
		if fv.IsNil() {
			fv.Set(reflect.New(fv.Type().Elem()))
		}
		return setField(fv.Elem(), raw)
	}
	if fv.CanAddr() && fv.Addr().Type().Implements(textUnmarshalerType) {
		return fv.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(raw))
	}
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(raw)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, fv.Type().Bits())
		if err != nil {
			return err
		}
		fv.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, fv.Type().Bits())
		if err != nil {
			return err
		}
		fv.SetUint(n)
	case reflect.Float32, reflect.Float64:
		// ParseFloat reads Go source syntax: digit separators ("1_000") and
		// hexadecimal floats ("0x1p4"). Ints and JSON bodies refuse both, and
		// no client sends them, so a parameter is a plain decimal number.
		if strings.ContainsAny(raw, "_xX") {
			return errors.New("not a decimal number")
		}
		n, err := strconv.ParseFloat(raw, fv.Type().Bits())
		if err != nil {
			return err
		}
		// "NaN" and "Inf" parse, but no JSON client can send them, NaN slips
		// through every numeric rule (it compares false with everything) and
		// neither can be written back as JSON.
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return errors.New("not a finite number")
		}
		fv.SetFloat(n)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		fv.SetBool(b)
	default:
		return fmt.Errorf("type %s is not supported", fv.Kind())
	}
	return nil
}

// Register registers a handler for an existing Endpoint definition.
// Useful for sharing one contract between server and client.
func Register[In, Out any](a *App, e Endpoint[In, Out], fn func(*Ctx, In) (Out, error), opts ...RouteOption) Endpoint[In, Out] {
	return Route(a, e.Method, e.Path, fn, opts...)
}
