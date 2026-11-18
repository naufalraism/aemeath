package aemeath

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// diffPatterns are regular expressions the Zod schemas carry over (or drop,
// when jsRegexp cannot translate them).
var diffPatterns = []string{
	`^[a-z]+$`, `^.$`, `^.{2,3}$`, `^\w+$`, `^\s*$`, `^\S+$`, `^\d{2}$`, `^[^a]$`, `^.+$`,
	`(?i)^k$`, `(?i)^ss$`, `(?s)^a.b$`, `(?m)^b$`, `(?m)^a$`, `^\bab\b$`, `^a|b$`, `^(?P<x>ab)+$`,
	`\Aab\z`, `^[à-ÿ]+$`, `^é$`, `^\x41$`, `^a{,2}$`, `^[\s]+$`, `^\.$`, `^[[:alpha:]]+$`,
	`^\pL+$`, `(?i)^straße$`, `^$`, `x`, `^[\d\s]+$`, `^a\nb$`, `^\t$`,
	`^[😀-😂]$`, `^\#\d+$`, `^\p{Greek}+$`, `^[[:upper:]][[:lower:]]*$`, `^\Q1+1\E$`, `^\101$`, `^[]a]+$`,
	`^\x{1F600}$`, `(?i)^[[:alpha:]]+$`, `^[^\s]+$`, `^\PL+$`, `^[\pL\d]+$`, `(?s)^.+$`, `^\w+@\w+$`, `}`, `^]$`,
}

type diffRoute struct {
	kind   reflect.Type
	tag    string
	quoted bool // json:",string"
}

func diffRoutes() []diffRoute {
	var r []diffRoute
	str := reflect.TypeFor[string]()
	for _, tag := range []string{"", "required", "min=2", "max=3", "len=2", "email", "url", "uuid", "oneof=a b ä 😀"} {
		r = append(r, diffRoute{str, tag, false})
	}
	for _, p := range diffPatterns {
		r = append(r, diffRoute{str, "pattern=" + p, false})
	}
	for _, k := range []reflect.Type{
		reflect.TypeFor[int8](), reflect.TypeFor[uint8](), reflect.TypeFor[int16](), reflect.TypeFor[uint16](),
		reflect.TypeFor[int32](), reflect.TypeFor[uint32](), reflect.TypeFor[int](), reflect.TypeFor[uint](),
		reflect.TypeFor[uint64](), reflect.TypeFor[float32](),
		reflect.TypeFor[float64](),
	} {
		for _, tag := range []string{"", "gt=0", "gte=-1,lte=5", "oneof=1 2 3", "lt=10"} {
			r = append(r, diffRoute{k, tag, false})
		}
	}
	for _, tag := range []string{"min=1", "max=2", "len=2"} {
		r = append(r, diffRoute{reflect.TypeFor[[]string](), tag, false})
		r = append(r, diffRoute{reflect.TypeFor[[]int8](), tag, false})
	}
	for _, tag := range []string{"required", "min=1", "max=1", "len=1"} {
		r = append(r, diffRoute{reflect.TypeFor[map[string]int](), tag, false})
	}
	for _, k := range []reflect.Type{reflect.TypeFor[int64](), reflect.TypeFor[uint64](), reflect.TypeFor[int8](), reflect.TypeFor[uint8](), reflect.TypeFor[*int32](), reflect.TypeFor[bool]()} {
		r = append(r, diffRoute{k, "", true})
	}
	r = append(r, diffRoute{reflect.TypeFor[[]string](), "required", false})
	r = append(r, diffRoute{reflect.TypeFor[*int](), "required", false}, diffRoute{reflect.TypeFor[*string](), "min=2", false}, diffRoute{reflect.TypeFor[bool](), "", false})
	return r
}

var diffRunes = []string{
	"a", "b", "k", "z", "A", "K", "S", "0", "7", " ", ".", "_", "-", "@", "/", ":",
	"\t", "\n", "\r", "\v", "\f", " ", " ", " ", "\ufeff", "　",
	"é", "é", "ä", "ß", "ẞ", "ſ", "K", "日", "😀", "😁", "𝒜", "\u200d",
}

func randomString(r *rand.Rand) string {
	n := r.IntN(5)
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(diffRunes[r.IntN(len(diffRunes))])
	}
	return b.String()
}

func diffValues(r *rand.Rand, k reflect.Type) []any {
	special := []any{nil, "", "a", "ab", "abc", "k", "ss", "straße", "STRASSE", "a\nb", "b", "a\nb\nc", "ab", "abab",
		"\t", "😀", "😁", "😂", "é", "x", "ana@example.com", "a@b", "ana@exämple.com", "https://example.com/x",
		"http://a", "javascript:alert(1)", "ftp://x", "00000000-0000-0000-0000-000000000000", "A", "AA", "12", "1 2",
		"ä", "a b", "+5", "05", "-0", "-5", "127", "128", "-128", "-129", "255", "256", "+255", "1e3", " 5", "5.0",
		"9223372036854775807", "9223372036854775808", "-9223372036854775808", "-9223372036854775809",
		"18446744073709551615", "18446744073709551616", "true", "false", true, false, []any{}, []any{"a"}, []any{"a", "b"}, []any{"a", "b", "c"}, []any{1, 2}, map[string]any{},
		map[string]any{"a": 1}, map[string]any{"a": 1, "b": 2}}
	var nums []any
	for _, f := range []float64{0, 1, -1, 2, 3, 4, 5, 6, 9, 10, 11, 0.5, 1.5, -1.5, 127, 128, -128, -129, 255, 256,
		32767, 32768, -32768, -32769, 65535, 65536, 2147483647, 2147483648, -2147483648, -2147483649,
		4294967295, 4294967296, 3.4e38, 3.5e38, 1e21, -1e21, 9007199254740991, math.MaxFloat64} {
		nums = append(nums, f)
	}
	vals := append(append([]any{}, special...), nums...)
	for i := 0; i < 400; i++ {
		vals = append(vals, randomString(r))
	}
	return vals
}

// The generated Zod schema and the server must accept and refuse the same
// values. Values are written as JSON.stringify writes them (what the
// generated client sends); integers beyond 2^53 are left out (documented).
func TestZodAgreesWithTheServer(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		skipNode(t, "node is not installed")
	}
	zodDir, _ := filepath.Abs("examples/tscheck/node_modules/zod")
	if !fileExistsForTest(zodDir) {
		skipNode(t, "zod is not installed (npm ci in examples/tscheck)")
	}
	r := rand.New(rand.NewPCG(1, 2))
	routes := diffRoutes()
	app := New()
	for i, rt := range routes {
		jsonTag := `json:"v"`
		if rt.quoted {
			jsonTag = `json:"v,string"`
		}
		typ := reflect.StructOf([]reflect.StructField{{Name: "V", Type: rt.kind, Tag: reflect.StructTag(jsonTag + ` validate:` + strconv.Quote(rt.tag))}})
		registerRuntimeRoute(app, "POST", fmt.Sprintf("/c%d", i), typ, reflect.TypeFor[struct{}]())
	}
	schemas := app.ZodSchemas()

	type tcase struct {
		Route  int    `json:"route"`
		Body   string `json:"body"`
		Server bool   `json:"server"`
	}
	var cases []tcase
	for i, rt := range routes {
		for _, v := range diffValues(r, rt.kind) {
			for _, body := range []string{bodyOf(v), "{}"} {
				rec := doJSON(app, "POST", fmt.Sprintf("/c%d", i), body)
				if rec.Code >= 500 {
					t.Fatalf("route %d (%s %q) body %s: %d", i, rt.kind, rt.tag, body, rec.Code)
				}
				cases = append(cases, tcase{i, body, rec.Code == 200})
			}
		}
	}

	dir := t.TempDir()
	js := strings.Replace(schemas, `import { z } from "zod";`, `const { z } = await import("`+fileURL(filepath.Join(zodDir, "index.js"))+`");`, 1)
	js = strings.ReplaceAll(js, "export const", "const")
	var names strings.Builder
	names.WriteString("\nconst schemaOf = {\n")
	for i := range routes {
		fmt.Fprintf(&names, "  %d: postC%dBodySchema,\n", i, i)
	}
	names.WriteString("};\n")
	casesJSON, _ := json.Marshal(cases)
	js += names.String() + `
const cases = JSON.parse(await (await import("node:fs/promises")).readFile(process.argv[2], "utf8"));
const out = cases.map(c => schemaOf[c.route].safeParse(JSON.parse(c.body)).success);
process.stdout.write(JSON.stringify(out));
`
	file, data := filepath.Join(dir, "diff.mjs"), filepath.Join(dir, "cases.json")
	if err := os.WriteFile(file, []byte(js), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(data, casesJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, file, data)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%.2000s", err, stderr.String())
	}
	var zod []bool
	if err := json.Unmarshal(out, &zod); err != nil {
		t.Fatalf("%v: %.300s", err, out)
	}

	// Group the disagreements by route and keep the shortest example.
	type group struct {
		count          int
		example        string
		server, client bool
	}
	groups := map[int]*group{}
	leftToServer := 0
	for i, c := range cases {
		if zod[i] == c.Server {
			continue
		}
		if pat, isPattern := strings.CutPrefix(routes[c.Route].tag, "pattern="); isPattern && !c.Server && zod[i] {
			if _, _, ok := jsRegexp(pat); !ok {
				// No faithful JavaScript form ((?m), ...): left out of Zod,
				// enforced by the server. Zod may only be more lenient.
				leftToServer++
				continue
			}
		}
		g := groups[c.Route]
		if g == nil {
			g = &group{}
			groups[c.Route] = g
		}
		g.count++
		if g.example == "" || len(c.Body) < len(g.example) {
			g.example, g.server, g.client = c.Body, c.Server, zod[i]
		}
	}
	keys := make([]int, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		g := groups[k]
		line := ""
		if i := strings.Index(schemas, fmt.Sprintf("postC%dBodySchema = z.object({\n", k)); i >= 0 {
			rest := schemas[i:]
			rest = rest[strings.Index(rest, "\n")+1:]
			line = strings.TrimSpace(rest[:strings.Index(rest, "\n")])
		}
		t.Errorf("%-8s %-24q %4d, e.g. %-26s server %-5v zod %-5v | %s",
			routes[k].kind, routes[k].tag, g.count, g.example, g.server, g.client, line)
	}
	t.Logf("pattern left to the server: %d", leftToServer)
	t.Logf("%d routes, %d cases, %d disagreements in %d routes", len(routes), len(cases), func() int {
		n := 0
		for _, g := range groups {
			n += g.count
		}
		return n
	}(), len(groups))
}

func bodyOf(v any) string {
	b, err := json.Marshal(map[string]any{"v": v})
	if err != nil {
		return "{}"
	}
	return string(b)
}
