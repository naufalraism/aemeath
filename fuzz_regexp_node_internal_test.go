package aemeath

import (
	"encoding/json"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var reAtoms = []string{
	"a", "b", "k", "K", "s", "ſ", "é", "😀", "日", " ", "\t", ".", `\.`, `\-`, `\#`, `\:`, `\_`, `\@`,
	"{", "}", "]", `\{`, `\}`, `\]`, `\[`, `\\`, `\/`, "/", "-", ",",
	`\s`, `\S`, `\w`, `\W`, `\d`, `\D`, `\b`, `\B`, `\pL`, `\p{Lu}`, `\p{Greek}`, `\PL`, `\p{^N}`, `\p{Any}`,
	`\x41`, `\x{1F600}`, `\101`, `\0`, `\t`, `\n`, `\r`, `\f`, `\v`, `\a`, `\Qa.b\E`, `\Q{]\E`,
	"[a-z]", "[^a]", `[\s]`, `[\d\s]`, `[^\s]`, "[[:alpha:]]", "[[:^digit:]]", "[]a]", "[^]a]", "[😀-😂]",
	`[\pL\d]`, `[a\-z]`, `[\#]`, "[-a]", "[a-]", `[\x41-\x43]`, `[\]]`, "[[]", "[.]", `[\w-]`,
}

var reQuant = []string{"", "", "", "*", "+", "?", "{2}", "{1,2}", "{,2}", "{2,}", "*?", "+?", "{0}"}

func randomPattern(r *rand.Rand, depth int) string {
	var b strings.Builder
	n := 1 + r.IntN(4)
	for i := 0; i < n; i++ {
		switch k := r.IntN(10); {
		case k < 7 || depth > 1:
			b.WriteString(reAtoms[r.IntN(len(reAtoms))])
		case k < 8:
			b.WriteString("(" + randomPattern(r, depth+1) + ")")
		case k < 9:
			b.WriteString("(?:" + randomPattern(r, depth+1) + "|" + randomPattern(r, depth+1) + ")")
		default:
			b.WriteString("(?P<g" + string(rune('a'+r.IntN(26))) + ">" + randomPattern(r, depth+1) + ")")
		}
		b.WriteString(reQuant[r.IntN(len(reQuant))])
	}
	return b.String()
}

// TestJSRegexpRandomPatterns: for thousands of random RE2 patterns, every
// translation jsRegexp returns must compile in JavaScript (a SyntaxError
// stops the whole schemas file from loading) and match exactly the strings
// Go matches.
func TestJSRegexpRandomPatterns(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		skipNode(t, "node is not installed")
	}
	r := rand.New(rand.NewPCG(7, 11))
	flagsets := []string{"", "", "(?i)", "(?s)", "(?is)", "(?m)"}
	type tc struct {
		Go, JS, Flags string
		Want          []bool
	}
	var subjects []string
	for i := 0; i < 120; i++ {
		subjects = append(subjects, randomString(r))
	}
	subjects = append(subjects, "", "a{,2}", "{", "}", "]", "a.b", "1+1", "#12", "😀", "😁", "\r", "\n", "a\nb", "ſ", "K", "_", "-", "/", "A", "\x07")
	var cases []tc
	tried, untranslated := 0, 0
	for len(cases) < 4000 {
		src := flagsets[r.IntN(len(flagsets))] + "^" + randomPattern(r, 0) + "$"
		if r.IntN(3) == 0 {
			src = strings.TrimSuffix(strings.TrimPrefix(src, "^"), "$")
		}
		re, err := regexp.Compile(src)
		tried++
		if err != nil {
			continue // refused at startup by the server
		}
		p, flags, ok := jsRegexp(src)
		if !ok {
			untranslated++
			continue
		}
		want := make([]bool, len(subjects))
		for i, s := range subjects {
			want[i] = re.MatchString(s)
		}
		cases = append(cases, tc{src, p, flags, want})
	}
	dir := t.TempDir()
	data, _ := json.Marshal(map[string]any{"cases": cases, "subjects": subjects})
	if err := os.WriteFile(filepath.Join(dir, "cases.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	js := `
const { cases, subjects } = JSON.parse(require("fs").readFileSync(process.argv[2], "utf8"));
const out = [];
for (const c of cases) {
  let re;
  try { re = new RegExp(c.JS, c.Flags); } catch (e) { out.push({ go: c.Go, js: c.JS, flags: c.Flags, error: e.message }); continue; }
  subjects.forEach((s, i) => { if (re.test(s) !== c.Want[i]) out.push({ go: c.Go, js: c.JS, flags: c.Flags, subject: s, server: c.Want[i] }); });
}
process.stdout.write(JSON.stringify(out));
`
	file := filepath.Join(dir, "check.cjs")
	if err := os.WriteFile(file, []byte(js), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, file, filepath.Join(dir, "cases.json")).Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var bad []map[string]any
	if err := json.Unmarshal(out, &bad); err != nil {
		t.Fatalf("%v: %.300s", err, out)
	}
	t.Logf("%d random patterns, %d valid in Go, %d translated and checked against %d strings each, %d left to the server",
		tried, len(cases)+untranslated, len(cases), len(subjects), untranslated)
	seen := map[string]bool{}
	for _, b := range bad {
		key, _ := b["go"].(string)
		if seen[key] {
			continue
		}
		seen[key] = true
		t.Errorf("%v", b)
		if len(seen) >= 15 {
			break
		}
	}
	if len(bad) > 0 {
		t.Errorf("%d disagreements in %d patterns", len(bad), len(seen))
	}
}
