//go:build !goexperiment.jsonv2

package aemeath

import (
	"strings"
	"unicode"
)

// jsonV2 reports whether encoding/json runs on encoding/json/v2, the
// default since Go 1.27 (GOEXPERIMENT=nojsonv2 turns it off). The two read
// names in json tags differently; see tagJSONName.
const jsonV2 = false

// tagJSONName is the name encoding/json takes from a json tag (without the
// "-" case), or "" when it uses the Go field name instead. The classic
// encoding/json keeps the text before the first comma when every character
// is a letter, a digit or allowed punctuation (isValidTag), and otherwise
// ignores the name.
func tagJSONName(tag string) string {
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		return ""
	}
	for _, c := range name {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
			// Backslash and quote are reserved; other punctuation is allowed.
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			return ""
		}
	}
	return name
}
