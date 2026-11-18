//go:build goexperiment.jsonv2

package aemeath

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// jsonV2 reports whether encoding/json runs on encoding/json/v2, the
// default since Go 1.27 (GOEXPERIMENT=nojsonv2 turns it off). The two read
// names in json tags differently; see tagJSONName.
const jsonV2 = true

// tagJSONName is the name encoding/json takes from a json tag (without the
// "-" case), or "" when it uses the Go field name instead. It follows
// parseFieldOptions in encoding/json/v2: almost any text before the first
// comma is the name ("price€", a control character, a space); when the text
// holds a reserved character (backslash, a quote, a backtick) the name is
// the run of letters, digits and underscores it starts with, and when it
// does not start with one, there is no name.
func tagJSONName(tag string) string {
	if tag == "" || tag[0] == ',' {
		return ""
	}
	n := len(tag) - len(strings.TrimLeftFunc(tag, func(r rune) bool {
		return !strings.ContainsRune(",\\'\"`", r)
	}))
	name := tag[:n]
	if !strings.HasPrefix(tag[n:], ",") && n != len(tag) {
		r, _ := utf8.DecodeRuneInString(tag)
		if r != '_' && !unicode.IsLetter(r) {
			return ""
		}
		name = tag[:len(tag)-len(strings.TrimLeftFunc(tag, func(r rune) bool {
			return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
		}))]
	}
	if !utf8.ValidString(name) {
		name = string([]rune(name))
	}
	return name
}
