package aemeath

import (
	"bufio"
	"encoding/json"
	"math/rand"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The server and the generated Zod schemas used different email, url and
// uuid checks (and Zod's own checks changed between its major versions). They
// now share the patterns in formats.go.
func TestSharedFormats(t *testing.T) {
	for s, want := range map[string]bool{
		"a@b.co": true, "first.last+tag@sub.example.com": true, "jösé@b.co": true, "用户@例子.广告": true, "o'neil@x.io": true,
		"a@b": false, "a@[127.0.0.1]": false, "Ann <a@b.co>": false, "a b@c.co": false, "a@b.co.": false,
		".a@b.co": false, "a..b@c.co": false, "a@b..co": false, "@b.co": false, "a@": false, "": false, "a@b.co\n": false, "a@@b.co": false,
	} {
		if got := validEmail(s); got != want {
			t.Errorf("email %q: %v, want %v", s, got, want)
		}
	}
	for s, want := range map[string]bool{
		"https://a.co": true, "http://localhost:8080/x?y=1#z": true, "HTTPS://A.CO/": true, "https://user:pw@a.co/p": true, "http://[::1]:80/": true,
		"ftp://a.co": false, "mailto:a@b.co": false, "javascript:alert(1)": false, "//a.co": false, "http://": false,
		"https://a.co/a b": false, "https:///path": false, " https://a.co": false, "https://a.co\n": false, "http://a.co/%zz": false, "": false,
	} {
		if got := validURL(s); got != want {
			t.Errorf("url %q: %v, want %v", s, got, want)
		}
	}
	for s, want := range map[string]bool{
		"123e4567-e89b-42d3-a456-426614174000": true, "123E4567-E89B-02D3-A456-426614174000": true, "00000000-0000-0000-0000-000000000000": true,
		"123e4567e89b42d3a456426614174000": false, "{123e4567-e89b-42d3-a456-426614174000}": false, "123e4567-e89b-42d3-a456-42661417400g": false,
	} {
		if got := validUUID(s); got != want {
			t.Errorf("uuid %q: %v, want %v", s, got, want)
		}
	}

	// The generators emit exactly these patterns.
	type in struct {
		E string `json:"e" validate:"email"`
		U string `json:"u" validate:"url"`
		I string `json:"i" validate:"uuid"`
	}
	a := New()
	POST(a, "/f", func(c *Ctx, v in) (in, error) { return v, nil })
	zod := a.ZodSchemas()
	spec, _ := json.Marshal(a.OpenAPI(Info{Title: "t", Version: "1"}))
	for _, p := range []string{emailPattern, urlPattern, uuidPattern} {
		quoted, _ := json.Marshal(p)
		if !strings.Contains(zod, strconv.Quote(p)) {
			t.Errorf("Zod does not use the shared pattern %s", p)
		}
		if !strings.Contains(string(spec), string(quoted[1:len(quoted)-1])) {
			t.Errorf("OpenAPI does not use the shared pattern %s", p)
		}
	}
	for _, old := range []string{".email()", ".url()", ".uuid()"} {
		if strings.Contains(zod, old) {
			t.Errorf("Zod still uses its own %s check", old)
		}
	}
}

// With FORMATS_FUZZ_OUT set, writes random strings and Go's verdicts for a
// JavaScript program to compare (see the commit message).
func TestWriteFormatCorpus(t *testing.T) {
	out := os.Getenv("FORMATS_FUZZ_OUT")
	if out == "" {
		t.Skip("set FORMATS_FUZZ_OUT to write the corpus")
	}
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	w := bufio.NewWriter(f)
	defer func() { _ = w.Flush() }()
	alphabet := []string{"a", "B", "9", ".", "..", "@", "-", "_", "+", "/", "//", ":", "?", "#", "%", "%2", "é", "用", "😀", " ", "\t", "\n", "\r", " ", " ", "\x00", "\x7f",
		"<", ">", "(", ")", "[", "]", ",", ";", "\"", "\\", "'", "http", "https", "HTTP", "://", "co", "com", "localhost", "127.0.0.1", "e89b", "0000", "g"}
	rng := rand.New(rand.NewSource(1))
	res := map[string]*regexp.Regexp{"email": emailRe, "url": urlRe, "uuid": uuidRe}
	enc := json.NewEncoder(w)
	seeds := []string{"a@b.co", "first.last+tag@sub.example.com", "jösé@b.co", "https://a.co", "http://localhost:8080/x?y=1#z",
		"HTTPS://user:pw@a.co/p", "http://[::1]:80/", "123e4567-e89b-42d3-a456-426614174000", "00000000-0000-0000-0000-000000000000"}
	for i := 0; i < 20000; i++ {
		var s string
		if i%2 == 0 { // random tokens
			var b strings.Builder
			for n := rng.Intn(9); n >= 0; n-- {
				b.WriteString(alphabet[rng.Intn(len(alphabet))])
			}
			s = b.String()
		} else { // a valid value with up to two random edits
			r := []rune(seeds[rng.Intn(len(seeds))])
			for n := rng.Intn(3); n > 0 && len(r) > 0; n-- {
				at := rng.Intn(len(r))
				switch rng.Intn(3) {
				case 0:
					r = append(r[:at], r[at+1:]...)
				case 1:
					r = append(r[:at], append([]rune(alphabet[rng.Intn(len(alphabet))]), r[at:]...)...)
				default:
					r[at] = []rune(alphabet[rng.Intn(len(alphabet))])[0]
				}
			}
			s = string(r)
		}
		_ = enc.Encode(map[string]any{"s": s, "email": res["email"].MatchString(s), "url": res["url"].MatchString(s), "uuid": res["uuid"].MatchString(s)})
	}
	_ = enc.Encode(map[string]any{"patterns": map[string]string{"email": emailPattern, "url": urlPattern, "uuid": uuidPattern}})
}
