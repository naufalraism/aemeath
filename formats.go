package aemeath

import (
	"net/url"
	"regexp"
)

// The email, url and uuid rules are defined here once, as regular
// expressions that mean the same in Go and in JavaScript. The server checks
// them, and the generators write the very same patterns into the Zod schemas
// and the OpenAPI document, so the browser and the server always agree,
// whatever version of Zod is installed (its own .email() and .uuid() checks
// changed between major versions).
const (
	// atom is a run of characters allowed in an email: anything except
	// control characters, spaces, "@", "." and the RFC 5322 specials.
	// Letters outside ASCII are allowed (internationalized addresses).
	emailAtom = `[^\x00-\x20\x7f@<>()\[\],;:"\\.]+`

	// emailPattern: local@domain.tld. The domain needs at least one dot, so
	// "a@b" is refused; address literals ("a@[127.0.0.1]") and display names
	// ("Ann <a@b.co>") are refused too.
	emailPattern = `^` + emailAtom + `(\.` + emailAtom + `)*@` + emailAtom + `(\.` + emailAtom + `)+$`

	// urlPattern: an absolute http or https URL with a host and no spaces or
	// control characters. Other schemes (javascript:, data:, ftp:) are refused.
	urlPattern = `^[hH][tT][tT][pP][sS]?://[^\x00-\x20\x7f/?#\\]+([/?#][^\x00-\x20\x7f]*)?$`

	// uuidPattern: 8-4-4-4-12 hexadecimal digits, any version.
	uuidPattern = `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`
)

var (
	emailRe = regexp.MustCompile(emailPattern)
	urlRe   = regexp.MustCompile(urlPattern)
	uuidRe  = regexp.MustCompile(uuidPattern)
)

func validEmail(s string) bool { return emailRe.MatchString(s) }
func validUUID(s string) bool  { return uuidRe.MatchString(s) }

// validURL also asks net/url, which refuses a few malformed URLs the pattern
// lets through (a bad percent-escape, a non-numeric port).
func validURL(s string) bool {
	if !urlRe.MatchString(s) {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && u.Host != ""
}
