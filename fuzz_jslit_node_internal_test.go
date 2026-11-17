package aemeath

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// edgeStrings are the strings most likely to be written wrongly into a
// JavaScript string literal.
func edgeStrings() []string {
	s := []string{
		"", "plain", `quo"te`, `back\slash`, "it's", "tab\there", "new\nline", "cr\rlf",
		"\a bell", "\v vtab", "\f feed", "\b back", "\x00 nul", "\x00" + "1", "\x1b esc", "\x7f del",
		"\u0080 c1", "\u00a0 nbsp", "\u00ad shy", "\u2028 ls", "\u2029 ps", "\u200b zwsp", "\u202e rlo",
		"\ufeff bom", "\uffff nonchar", "\ue000 private", "😀 emoji", "\U0001d173 musical format",
		"\U000e0001 language tag", "\U000f0000 plane 15 private", "\U0010ffff max",
		"</script>", "${x}", "`tick`", "café", "日本",
		"\xff invalid byte", "a\xc3", "\xed\xa0\x80 surrogate encoded",
	}
	for r := rune(0); r < 0x100; r++ {
		s = append(s, string(r))
	}
	return s
}

// TestJSStringInARealEngine checks jsString, which the generators use to
// write string literals into TypeScript and Zod, against a real JavaScript
// engine, and checks decodeJSString (the oracle of FuzzJSString) against the
// same engine. strconv.Quote, used before, changed 9 of these strings.
func TestJSStringInARealEngine(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	strs := edgeStrings()
	var js strings.Builder
	js.WriteString("const lits = [\n")
	for _, s := range strs {
		js.WriteString("  " + jsString(s) + ",\n")
	}
	js.WriteString("];\nprocess.stdout.write(JSON.stringify(lits.map(s => Array.from({length: s.length}, (_, i) => s.charCodeAt(i)))));\n")
	file := filepath.Join(t.TempDir(), "lits.js")
	if err := os.WriteFile(file, []byte(js.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, file).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var units [][]uint16
	if err := json.Unmarshal(out, &units); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	wrong := 0
	for i, s := range strs {
		got := string(utf16.Decode(units[i]))
		mine, derr := decodeJSString(jsString(s))
		if derr != nil || mine != got {
			t.Errorf("decodeJSString disagrees with node for %q: mine %q (%v), node %q", s, mine, derr, got)
		}
		want := string([]rune(s)) // each invalid byte becomes U+FFFD, as encoding/json sends it
		if got != want {
			wrong++
			t.Errorf("jsString(%q) = %s, which JavaScript reads as %q", s, jsString(s), got)
		}
	}
	t.Logf("%d strings checked in node, %d changed meaning", len(strs), wrong)
}

// jsString must write every string so that JavaScript reads it back (each
// invalid byte as U+FFFD). decodeJSString is checked against node above.
func FuzzJSString(f *testing.F) {
	for _, s := range edgeStrings() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		lit := jsString(s)
		got, err := decodeJSString(lit)
		if err != nil {
			t.Fatalf("jsString(%q) = %s: not a valid JavaScript literal: %v", s, lit, err)
		}
		if want := string([]rune(s)); got != want {
			t.Fatalf("jsString(%q) = %s, read back as %q", s, lit, got)
		}
		if strings.ContainsAny(lit, "\n\r\u2028\u2029") {
			t.Fatalf("jsString(%q) = %s contains a raw line break", s, lit)
		}
	})
}
