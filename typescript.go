package aemeath

import (
	"fmt"
	"io"
	"log"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// TypeScriptClient menghasilkan source TypeScript (client bertipe aman)
// dari semua route bertipe yang terdaftar.
func (a *App) TypeScriptClient() string {
	g := newTSGen()
	for _, s := range a.specs {
		g.scanInput(s.in, s.method)
		g.scanOutput(s.out)
	}
	g.assignNames()

	var ops []string
	usedOps := map[string]bool{}
	for _, s := range a.specs {
		base := tsIdent(s.opID())
		name := base
		for i := 2; usedOps[name]; i++ {
			name = base + strconv.Itoa(i)
		}
		if name != base {
			log.Printf("aemeath: peringatan: nama operasi %q bentrok, menggunakan %q", base, name)
		}
		usedOps[name] = true
		ops = append(ops, g.operation(s, name))
	}

	var b strings.Builder
	b.WriteString("// Dibuat otomatis oleh Aemeath. Jangan diedit manual.\n/* eslint-disable */\n\n")
	for _, e := range g.order {
		if e.inName != "" {
			fmt.Fprintf(&b, "export interface %s {\n", e.inName)
			for _, line := range g.fields(e.typ, 0, true) {
				fmt.Fprintf(&b, "  %s;\n", line)
			}
			b.WriteString("}\n\n")
		}
		if e.outName != "" {
			fmt.Fprintf(&b, "export interface %s {\n", e.outName)
			for _, line := range g.fields(e.typ, 0, false) {
				fmt.Fprintf(&b, "  %s;\n", line)
			}
			b.WriteString("}\n\n")
		}
	}
	b.WriteString(tsRuntime)
	b.WriteString("\nexport function createClient(opts: ClientOptions) {\n  return {\n")
	for _, op := range ops {
		b.WriteString(op)
	}
	b.WriteString("  };\n}\n\nexport type Client = ReturnType<typeof createClient>;\n")
	return b.String()
}

// WriteTypeScriptClient menulis client TypeScript ke file.
func (a *App) WriteTypeScriptClient(path string) error {
	return os.WriteFile(path, []byte(a.TypeScriptClient()), 0o644)
}

// EnableClient mendaftarkan GET /client.ts.
func (a *App) EnableClient() {
	var (
		mu          sync.Mutex
		cached      string
		cachedCount int
	)
	a.Handle("GET", "/client.ts", func(c *Ctx) error {
		mu.Lock()
		if cachedCount != len(a.specs) {
			cached = a.TypeScriptClient()
			cachedCount = len(a.specs)
		}
		out := cached
		mu.Unlock()
		c.W.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, err := io.WriteString(c.W, out)
		return err
	})
}

const tsRuntime = `export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
    this.name = "ApiError";
  }
}

export interface ClientOptions {
  baseUrl: string;
  fetch?: typeof fetch;
  headers?: Record<string, string> | (() => Record<string, string>);
}

interface RequestInput {
  path?: Record<string, unknown>;
  query?: Record<string, unknown>;
  body?: unknown;
}

async function request<T>(opts: ClientOptions, method: string, path: string, input: RequestInput): Promise<T> {
  const url = path.replace(/\{([^}]+)\}/g, (_, k: string) => encodeURIComponent(String(input.path?.[k])));
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(input.query ?? {})) {
    if (v !== undefined && v !== null) qs.set(k, String(v));
  }
  const q = qs.toString();
  const extra = typeof opts.headers === "function" ? opts.headers() : opts.headers;
  const f = opts.fetch ?? fetch;
  const res = await f(opts.baseUrl.replace(/\/$/, "") + url + (q ? "?" + q : ""), {
    method,
    headers: { ...(input.body !== undefined ? { "Content-Type": "application/json" } : {}), ...extra },
    body: input.body !== undefined ? JSON.stringify(input.body) : undefined,
  });
  const text = await res.text();
  const data = text ? JSON.parse(text) : undefined;
  if (!res.ok) throw new ApiError(res.status, data?.error ?? res.statusText);
  return data as T;
}
`

type tsEntry struct {
	typ     reflect.Type
	useIn   bool
	useOut  bool
	inName  string
	outName string
}

type tsGen struct {
	entries map[reflect.Type]*tsEntry
	order   []*tsEntry
	used    map[string]bool
}

func newTSGen() *tsGen {
	return &tsGen{
		entries: map[reflect.Type]*tsEntry{},
		used:    map[string]bool{"ApiError": true, "ClientOptions": true, "Client": true},
	}
}

func (g *tsGen) scanInput(t reflect.Type, method string) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		src, _ := paramSource(f)
		if src != "" {
			g.scanType(f.Type, true)
			continue
		}
		if bodyMethod(method) {
			g.scanType(f.Type, true)
		}
	}
}

func (g *tsGen) scanOutput(t reflect.Type) { g.scanType(t, false) }

func (g *tsGen) scanType(t reflect.Type, input bool) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == timeType {
		return
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		g.scanType(t.Elem(), input)
		return
	case reflect.Struct:
	default:
		return
	}
	if t.Name() == "" {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() || jsonName(f) == "-" {
				continue
			}
			g.scanType(f.Type, input)
		}
		return
	}
	e, ok := g.entries[t]
	if !ok {
		e = &tsEntry{typ: t}
		g.entries[t] = e
		g.order = append(g.order, e)
	}
	if input {
		if e.useIn {
			return
		}
		e.useIn = true
	} else {
		if e.useOut {
			return
		}
		e.useOut = true
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() || jsonName(f) == "-" {
			continue
		}
		g.scanType(f.Type, input)
	}
}

func (g *tsGen) assignNames() {
	for _, e := range g.order {
		base := upperFirst(tsIdent(e.typ.Name()))
		if base == "" {
			base = "Anonymous"
		}
		both := e.useIn && e.useOut
		if e.useIn {
			name := base
			if both {
				name = base + "In"
			}
			e.inName = g.uniqueName(name)
		}
		if e.useOut {
			name := base
			if both {
				name = base + "Out"
			}
			e.outName = g.uniqueName(name)
		}
	}
}

func (g *tsGen) uniqueName(base string) string {
	name := base
	for i := 2; g.used[name]; i++ {
		name = base + strconv.Itoa(i)
	}
	g.used[name] = true
	return name
}

func (g *tsGen) register(t reflect.Type, input bool) string {
	e := g.entries[t]
	if input {
		return e.inName
	}
	return e.outName
}

func (g *tsGen) typeRef(t reflect.Type, depth int, input bool) string {
	if depth > maxSchemaDepth {
		return "unknown"
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == timeType {
		return "string"
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return "string"
		}
		return "Array<" + g.typeRef(t.Elem(), depth+1, input) + ">"
	case reflect.Array:
		return "Array<" + g.typeRef(t.Elem(), depth+1, input) + ">"
	case reflect.Map:
		return "Record<string, " + g.typeRef(t.Elem(), depth+1, input) + ">"
	case reflect.Struct:
		if t.Name() != "" {
			return g.register(t, input)
		}
		lines := g.fields(t, depth+1, input)
		if len(lines) == 0 {
			return "Record<string, never>"
		}
		return "{ " + strings.Join(lines, "; ") + " }"
	}
	return "unknown"
}

func (g *tsGen) fields(t reflect.Type, depth int, input bool) []string {
	var lines []string
	for i := 0; i < t.NumField(); i++ {
		if line, ok := g.field(t.Field(i), depth, input); ok {
			lines = append(lines, line)
		}
	}
	return lines
}

func (g *tsGen) field(f reflect.StructField, depth int, input bool) (string, bool) {
	if !f.IsExported() {
		return "", false
	}
	name := jsonName(f)
	if name == "-" {
		return "", false
	}
	typ := g.typeRef(f.Type, depth, input)
	if f.Type.Kind() == reflect.Pointer {
		typ += " | null"
	}
	opt := hasOmitEmpty(f)
	if input {
		opt = !hasRequired(f)
	}
	return tsField(name, typ, opt), true
}

func (g *tsGen) operation(s routeSpec, name string) string {
	t := s.in
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	var pathP, queryP, bodyP []string
	queryReq, bodyReq := false, false
	declared := map[string]bool{}
	if t.Kind() == reflect.Struct {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			src, pname := paramSource(f)
			switch {
			case src == "path":
				pathP = append(pathP, tsField(pname, g.typeRef(f.Type, 1, true), false))
				declared[pname] = true
			case src == "query":
				opt := !hasRequired(f)
				queryP = append(queryP, tsField(pname, g.typeRef(f.Type, 1, true), opt))
				if !opt {
					queryReq = true
				}
			case bodyMethod(s.method):
				if line, ok := g.field(f, 1, true); ok {
					bodyP = append(bodyP, line)
					if hasRequired(f) {
						bodyReq = true
					}
				}
			}
		}
	}
	for _, m := range pathParamRe.FindAllStringSubmatch(s.path, -1) {
		if !declared[m[1]] {
			pathP = append(pathP, tsField(m[1], "string", false))
			declared[m[1]] = true
		}
	}

	var segs []string
	needInput := false
	if len(pathP) > 0 {
		segs = append(segs, "path: { "+strings.Join(pathP, "; ")+" }")
		needInput = true
	}
	if len(queryP) > 0 {
		segs = append(segs, "query"+optMark(!queryReq)+": { "+strings.Join(queryP, "; ")+" }")
		needInput = needInput || queryReq
	}
	if len(bodyP) > 0 {
		segs = append(segs, "body"+optMark(!bodyReq)+": { "+strings.Join(bodyP, "; ")+" }")
		needInput = needInput || bodyReq
	}
	sig, arg := "", "{}"
	if len(segs) > 0 {
		obj := "{ " + strings.Join(segs, "; ") + " }"
		if needInput {
			sig, arg = "input: "+obj, "input"
		} else {
			sig, arg = "input?: "+obj, "input ?? {}"
		}
	}

	var b strings.Builder
	if s.cfg.summary != "" {
		doc := strings.NewReplacer("*/", "* /", "\n", " ").Replace(s.cfg.summary)
		fmt.Fprintf(&b, "    /** %s */\n", doc)
	}
	out := g.typeRef(s.out, 0, false)
	fmt.Fprintf(&b, "    %s: (%s) =>\n", name, sig)
	fmt.Fprintf(&b, "      request<%s>(opts, %s, %s, %s),\n", out, strconv.Quote(s.method), strconv.Quote(openAPIPath(s.path)), arg)
	return b.String()
}

func optMark(opt bool) string {
	if opt {
		return "?"
	}
	return ""
}

func tsField(name, typ string, opt bool) string {
	return tsKey(name) + optMark(opt) + ": " + typ
}

var tsKeyRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

func tsKey(name string) string {
	if tsKeyRe.MatchString(name) {
		return name
	}
	return strconv.Quote(name)
}

// hasOmitEmpty dipakai juga oleh route.go — jangan didefinisikan ulang di sana.
func hasOmitEmpty(f reflect.StructField) bool {
	_, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
	for _, o := range strings.Split(opts, ",") {
		if o == "omitempty" || o == "omitzero" {
			return true
		}
	}
	return false
}

func (s routeSpec) opID() string {
	if s.cfg.opID != "" {
		return s.cfg.opID
	}
	var b strings.Builder
	b.WriteString(strings.ToLower(s.method))
	trimmed := strings.Trim(openAPIPath(s.path), "/")
	if trimmed == "" {
		b.WriteString("Root")
	}
	for _, seg := range strings.Split(trimmed, "/") {
		if seg == "" {
			continue
		}
		if m := pathParamRe.FindStringSubmatch(seg); m != nil && m[0] == seg {
			fmt.Fprintf(&b, "By%s", pascal(m[1]))
			continue
		}
		b.WriteString(pascal(seg))
	}
	return b.String()
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func pascal(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(upperFirst(p))
	}
	return b.String()
}

func tsIdent(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_') {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" || unicode.IsDigit(rune(out[0])) {
		out = "op" + out
	}
	return out
}
