package aemeath

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
)

// jsString writes s as a JavaScript (and TypeScript) string literal.
//
// strconv.Quote writes Go syntax, which is not JavaScript: "\a" and the
// "\U0001xxxx" form of non-printable characters above U+FFFF mean other
// strings there ("\U000e0067", a part of the Scottish flag emoji, reads as
// "U000e0067"), and "\xff" for an invalid byte is the character U+00FF.
// Here printable characters are written as they are, quotes, backslashes and
// control or invisible characters are escaped as \n, \t, ... or \uXXXX
// (surrogate pairs above U+FFFF), and invalid UTF-8 becomes U+FFFD, which is
// also what encoding/json sends for it.
func jsString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s { // invalid bytes come out as U+FFFD
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\v':
			b.WriteString(`\v`)
		default:
			if unicode.IsPrint(r) && r != ' ' && r != ' ' {
				b.WriteRune(r)
				continue
			}
			if r > 0xFFFF {
				hi, lo := utf16.EncodeRune(r)
				writeU(&b, hi)
				writeU(&b, lo)
				continue
			}
			writeU(&b, r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func writeU(b *strings.Builder, r rune) {
	h := strconv.FormatInt(int64(r), 16)
	b.WriteString(`\u` + strings.Repeat("0", 4-len(h)) + h)
}
