package securityaudit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/session"
)

func TestSession_FixationAndCookieFlags(t *testing.T) {
	store := session.NewMemoryStore()
	app := aemeath.New()
	app.Use(session.Middleware(session.Config{Store: store}))
	app.Handle("POST", "/login", func(c *aemeath.Ctx) error {
		s := session.From(c)
		_ = s.Renew(c.Context())
		s.Set("user", "ann")
		return c.Text(200, "ok")
	})
	app.Handle("GET", "/me", func(c *aemeath.Ctx) error { return c.Text(200, "user="+session.From(c).String("user")) })

	// Attacker plants a 43-character id of their choice before the victim logs in.
	planted := strings.Repeat("A", 43)
	r := httptest.NewRequest("POST", "/login", nil)
	r.AddCookie(&http.Cookie{Name: "__Host-aemeath_session", Value: planted})
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	set := w.Header().Get("Set-Cookie")
	t.Logf("Set-Cookie after login with a planted id: %s", set)
	if strings.Contains(set, planted) {
		t.Error("WEAKNESS: session fixation: the planted id was adopted")
	}
	for _, flag := range []string{"HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(set, flag) {
			t.Errorf("WEAKNESS: session cookie lacks %s", flag)
		}
	}
	// The planted id must not give access afterwards.
	r = httptest.NewRequest("GET", "/me", nil)
	r.AddCookie(&http.Cookie{Name: "__Host-aemeath_session", Value: planted})
	w = httptest.NewRecorder()
	app.ServeHTTP(w, r)
	t.Logf("GET /me with the planted id -> %q", w.Body.String())
	if strings.Contains(w.Body.String(), "ann") {
		t.Error("WEAKNESS: the planted id reads the victim's session")
	}
}
