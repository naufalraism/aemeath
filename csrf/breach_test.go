package csrf_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/csrf"
	"github.com/naufalraism/aemeath/session"
)

// BREACH: when a compressed response holds a secret and also echoes what
// an attacker sent, the size of the response reveals the secret byte by
// byte. A token that is the same in every response is such a secret. Each
// response must carry a different token, made from the session's secret and
// fresh random bytes, so there is nothing stable to guess.
func TestTokenIsDifferentInEveryResponse(t *testing.T) {
	app := newApp(csrf.Config{Cookie: "XSRF-TOKEN", Insecure: true})
	// The secret itself, as the session stores it (the old token format).
	aemeath.GET(app, "/secret", func(c *aemeath.Ctx, _ none) (tok, error) {
		return tok{session.From(c).String("aemeath.csrf")}, nil
	})
	sess, t1 := start(app)
	t2 := tokenIn(call(app, "GET", "/token", sess, nil).Body.String())
	secret := tokenIn(call(app, "GET", "/secret", sess, nil).Body.String())
	if secret == "" {
		t.Fatal("no secret in the session")
	}

	if t1 == t2 {
		t.Fatalf("two responses carried the same token %q", t1)
	}
	for _, tk := range []string{t1, t2} {
		if strings.Contains(tk, secret) {
			t.Errorf("the token %q contains the session's secret", tk)
		}
		if code := call(app, "POST", "/act", sess, map[string]string{"X-CSRF-Token": tk}).Code; code != 200 {
			t.Errorf("token %q: status %d, want 200", tk, code)
		}
	}

	// The readable cookie must not hold the secret either.
	w := call(app, "GET", "/token", sess, nil)
	for _, c := range w.Result().Cookies() {
		if c.Name == "XSRF-TOKEN" {
			if c.Value == secret {
				t.Error("the XSRF-TOKEN cookie holds the session's secret")
			}
			if code := call(app, "POST", "/act", sess, map[string]string{"X-CSRF-Token": c.Value}).Code; code != 200 {
				t.Errorf("the cookie's token: status %d, want 200", code)
			}
		}
	}

	// A page opened before the upgrade still holds the old token: the secret.
	if code := call(app, "POST", "/act", sess, map[string]string{"X-CSRF-Token": secret}).Code; code != 200 {
		t.Errorf("a token in the old format: status %d, want 200", code)
	}

	// Anything else is refused: one changed character in either half, a
	// token of another session, other lengths.
	other, otherTok := start(app)
	_ = other
	refused := map[string]string{
		"first half changed":  flip(t1, 0),
		"second half changed": flip(t1, len(t1)-1),
		"another session":     otherTok,
		"too short":           t1[:len(t1)-2],
		"too long":            t1 + "AA",
		"not base64":          strings.Repeat("!", len(t1)),
		"stray bits at end":   strayBits(t1),
		"empty":               "",
	}
	for name, tk := range refused {
		if code := call(app, "POST", "/act", sess, map[string]string{"X-CSRF-Token": tk}).Code; code != http.StatusForbidden {
			t.Errorf("%s (%q): status %d, want 403", name, tk, code)
		}
	}
}

// flip changes the character at i to another base64url character.
func flip(s string, i int) string {
	c := byte('A')
	if s[i] == 'A' {
		c = 'B'
	}
	return s[:i] + string(c) + s[i+1:]
}

// strayBits changes only the unused low bits of the last character: 64
// bytes are 86 base64 characters, and the last one carries 4 bits of data.
// A lenient decoder reads the same bytes; the token must still be refused,
// or one token would have several spellings.
func strayBits(s string) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	v := strings.IndexByte(alphabet, s[len(s)-1])
	return s[:len(s)-1] + string(alphabet[v^1])
}
