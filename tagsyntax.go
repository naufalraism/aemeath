package aemeath

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// frameworkTagKeys are the struct tag keys Aemeath reads.
var frameworkTagKeys = []string{"json", "validate", "query", "path", "header", "cookie", "form", "aemeath_json"}

// checkTagSyntax panics when a struct tag Aemeath reads cannot be parsed.
// reflect.StructTag.Get returns "" for such a key, so the field would lose
// all its rules without a word: `validate:"required,pattern=^\d{5}$"` is not
// a valid Go string literal (\d is not an escape), so the field is neither
// required nor checked, on the server, in the Zod schemas and in OpenAPI.
// go vet reports it; this makes it fail at startup like any other bad rule.
func checkTagSyntax(owner reflect.Type, f reflect.StructField) {
	if key, why := badTagKey(f.Tag); key != "" {
		panic(fmt.Sprintf("aemeath: field %s.%s: the struct tag cannot be read (%s), so its `%s` part would be ignored; "+
			"a backslash in a tag must be doubled (\\\\d) or replaced ([0-9]): %s", owner.Name(), f.Name, why, key, f.Tag))
	}
}

// badTagKey follows the parsing of reflect.StructTag.Lookup and returns the
// first key Aemeath reads that Lookup would miss, with the reason.
func badTagKey(tag reflect.StructTag) (key, why string) {
	s := string(tag)
	for s != "" {
		i := 0
		for i < len(s) && s[i] == ' ' {
			i++
		}
		s = s[i:]
		if s == "" {
			break
		}
		i = 0
		for i < len(s) && s[i] > ' ' && s[i] != ':' && s[i] != '"' && s[i] != 0x7f {
			i++
		}
		if i == 0 || i+1 >= len(s) || s[i] != ':' || s[i+1] != '"' {
			return firstKeyIn(s, "bad syntax")
		}
		name := s[:i]
		s = s[i+1:]
		i = 1
		for i < len(s) && s[i] != '"' {
			if s[i] == '\\' {
				i++
			}
			i++
		}
		if i >= len(s) {
			return firstKeyIn(name+":"+s, "unterminated quoted value")
		}
		quoted := s[:i+1]
		s = s[i+1:]
		if !isFrameworkKey(name) {
			continue
		}
		if _, err := strconv.Unquote(quoted); err != nil {
			return name, "invalid escape in " + quoted
		}
	}
	return "", ""
}

// firstKeyIn returns the first framework key mentioned in the unparsable rest
// of a tag: Lookup stops at the error, so every key after it is lost.
func firstKeyIn(rest, why string) (string, string) {
	best, at := "", -1
	for _, k := range frameworkTagKeys {
		if i := strings.Index(rest, k+":"); i >= 0 && (at < 0 || i < at) {
			best, at = k, i
		}
	}
	if best == "" {
		return "", ""
	}
	return best, why
}

func isFrameworkKey(name string) bool {
	for _, k := range frameworkTagKeys {
		if k == name {
			return true
		}
	}
	return false
}
