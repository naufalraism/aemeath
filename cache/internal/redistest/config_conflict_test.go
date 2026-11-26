package redistest

import (
	"strings"
	"testing"

	"github.com/naufalraism/aemeath/cache"
)

func panicOf(f func()) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg, _ = r.(string)
			if msg == "" {
				msg = "panic"
			}
		}
	}()
	f()
	return ""
}

// With URL set, Addr, Password and DB were ignored without a word: a URL
// without a password next to Password, a common way to keep the secret out
// of the URL, connected without it and failed in production.
func TestURLCannotBeCombinedWithTheFieldsItReplaces(t *testing.T) {
	for name, cfg := range map[string]cache.Config{
		"Addr":     {URL: "redis://localhost:6379/0", Addr: "other:6379"},
		"Password": {URL: "redis://localhost:6379/0", Password: "secret"},
		"DB":       {URL: "redis://localhost:6379/0", DB: 2},
	} {
		msg := panicOf(func() { cache.New(cfg) })
		if !strings.Contains(msg, name) || strings.Contains(msg, "secret") {
			t.Errorf("URL with %s: panic %q, want a message naming %s (and not the password)", name, msg, name)
		}
	}
	for name, cfg := range map[string]cache.Config{
		"URL alone":  {URL: "redis://:pw@localhost:6379/1"},
		"Addr alone": {Addr: "localhost:6379", Password: "pw", DB: 1},
	} {
		if msg := panicOf(func() { cache.New(cfg) }); msg != "" {
			t.Errorf("%s panicked: %q", name, msg)
		}
	}
}
