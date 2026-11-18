package aemeath

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// decodeJSString decodes a JavaScript string literal the way a JavaScript
// engine does (ECMAScript, non-strict): \b \f \n \r \t \v \0 \xHH \uHHHH
// \u{H...} are escapes, a backslash before a line terminator continues the
// line, and a backslash before any other character is that character
// ("\a" is "a", "\U" is "U"). The result is the UTF-16 string as Go runes.
func decodeJSString(lit string) (string, error) {
	if len(lit) < 2 || (lit[0] != '"' && lit[0] != '\'') || lit[len(lit)-1] != lit[0] {
		return "", errors.New("not a string literal")
	}
	q := lit[0]
	s := lit[1 : len(lit)-1]
	var units []uint16
	add := func(r rune) {
		units = append(units, utf16.Encode([]rune{r})...)
	}
	for i := 0; i < len(s); {
		c := s[i]
		if c == q {
			return "", errors.New("unescaped quote inside the literal")
		}
		if c == '\n' || c == '\r' {
			return "", errors.New("raw line break inside the literal")
		}
		if c != '\\' {
			r, size := decodeRune(s[i:]) // U+2028 and U+2029 are allowed raw since ES2019
			add(r)
			i += size
			continue
		}
		i++
		if i >= len(s) {
			return "", errors.New("trailing backslash")
		}
		e := s[i]
		i++
		switch e {
		case 'b':
			add('\b')
		case 'f':
			add('\f')
		case 'n':
			add('\n')
		case 'r':
			add('\r')
		case 't':
			add('\t')
		case 'v':
			add('\v')
		case '0':
			if i < len(s) && s[i] >= '0' && s[i] <= '9' {
				return "", errors.New("legacy octal escape")
			}
			add(0)
		case '1', '2', '3', '4', '5', '6', '7', '8', '9':
			return "", errors.New("legacy octal escape \\" + string(e))
		case 'x':
			if i+2 > len(s) {
				return "", errors.New("short \\x")
			}
			v, err := strconv.ParseUint(s[i:i+2], 16, 8)
			if err != nil {
				return "", err
			}
			add(rune(v))
			i += 2
		case 'u':
			if i < len(s) && s[i] == '{' {
				end := strings.IndexByte(s[i:], '}')
				if end < 0 {
					return "", errors.New("unterminated \\u{")
				}
				v, err := strconv.ParseUint(s[i+1:i+end], 16, 32)
				if err != nil || v > 0x10FFFF {
					return "", errors.New("bad \\u{}")
				}
				add(rune(v))
				i += end + 1
				continue
			}
			if i+4 > len(s) {
				return "", errors.New("short \\u")
			}
			v, err := strconv.ParseUint(s[i:i+4], 16, 16)
			if err != nil {
				return "", err
			}
			units = append(units, uint16(v))
			i += 4
		case '\n':
		case '\r':
			if i < len(s) && s[i] == '\n' {
				i++
			}
		default:
			// Any other character after a backslash stands for itself.
			r, size := decodeRune(s[i-1:])
			add(r)
			i += size - 1
		}
	}
	return string(utf16.Decode(units)), nil
}

// decodeRune reads one UTF-8 sequence; an invalid byte becomes U+FFFD, as a
// JavaScript engine reading the file as UTF-8 does.
func decodeRune(s string) (rune, int) { return utf8.DecodeRuneInString(s) }
