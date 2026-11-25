package session_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/session"
)

func sessionCookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, ck := range w.Result().Cookies() {
		if ck.Name == name {
			return ck
		}
	}
	return nil
}

// Session fixation: an attacker gets a real session id by visiting the site,
// plants it in the victim's browser, and waits for the victim to log in. If
// the login keeps the id, the attacker's cookie is now logged in as the
// victim. A login written with s.Set alone did exactly that; s.Login renews
// the id first.
func TestLoginRotatesTheSessionID(t *testing.T) {
	for _, tc := range []struct {
		name     string
		login    func(c *aemeath.Ctx, s *session.Session) error
		attacker string // what the attacker's cookie sees afterwards
	}{
		{"Set alone (the mistake Login prevents)", func(_ *aemeath.Ctx, s *session.Session) error {
			s.Set("user", "victim")
			return nil
		}, "me=victim"},
		{"Login", func(c *aemeath.Ctx, s *session.Session) error {
			return s.Login(c.Context(), "user", "victim")
		}, "me="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := session.NewMemoryStore()
			app := aemeath.New()
			app.Use(session.Middleware(session.Config{Store: store, Insecure: true}))
			app.Handle("GET", "/visit", func(c *aemeath.Ctx) error { session.From(c).Set("seen", true); return c.Text(200, "ok") })
			app.Handle("GET", "/login", func(c *aemeath.Ctx) error {
				if err := tc.login(c, session.From(c)); err != nil {
					return err
				}
				return c.Text(200, "logged in")
			})
			app.Handle("GET", "/me", func(c *aemeath.Ctx) error { return c.Text(200, "me="+session.From(c).String("user")) })

			w := httptest.NewRecorder()
			app.ServeHTTP(w, httptest.NewRequest("GET", "/visit", nil))
			planted := sessionCookie(w, "aemeath_session")

			r := httptest.NewRequest("GET", "/login", nil) // the victim, with the planted cookie
			r.AddCookie(&http.Cookie{Name: planted.Name, Value: planted.Value})
			w = httptest.NewRecorder()
			app.ServeHTTP(w, r)
			victim := sessionCookie(w, "aemeath_session")

			r = httptest.NewRequest("GET", "/me", nil) // the attacker, with the same cookie
			r.AddCookie(&http.Cookie{Name: planted.Name, Value: planted.Value})
			w = httptest.NewRecorder()
			app.ServeHTTP(w, r)
			if got := w.Body.String(); got != tc.attacker {
				t.Errorf("the attacker's cookie sees %q, want %q", got, tc.attacker)
			}
			if tc.attacker == "me=" {
				if victim == nil || victim.Value == planted.Value {
					t.Fatal("Login did not give the victim a new session id")
				}
				if _, ok, _ := store.Load(context.Background(), planted.Value); ok {
					t.Error("the planted session still exists after Login")
				}
				r = httptest.NewRequest("GET", "/me", nil)
				r.AddCookie(&http.Cookie{Name: victim.Name, Value: victim.Value})
				w = httptest.NewRecorder()
				app.ServeHTTP(w, r)
				if w.Body.String() != "me=victim" {
					t.Errorf("the victim's new cookie sees %q, want me=victim", w.Body.String())
				}
			}
		})
	}
}

// Over HTTPS the cookie is named __Host-aemeath_session. A browser accepts a
// __Host- cookie only when it is Secure, has Path=/ and no Domain, and only
// from that host itself, so a sibling subdomain or a plain-http page cannot
// plant one: the fixation above cannot start.
func TestCookieNameDefaultsToHostPrefix(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  session.Config
		want string
	}{
		{"default (Secure)", session.Config{}, "__Host-aemeath_session"},
		{"Insecure (plain-http localhost)", session.Config{Insecure: true}, "aemeath_session"},
		{"Domain set (shared with subdomains)", session.Config{Domain: "example.com"}, "aemeath_session"},
		{"Path not /", session.Config{Path: "/app"}, "aemeath_session"},
		{"CookieName set", session.Config{CookieName: "sid"}, "sid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			cfg.Store = session.NewMemoryStore()
			app := aemeath.New()
			app.Use(session.Middleware(cfg))
			app.Handle("GET", "/set", func(c *aemeath.Ctx) error { session.From(c).Set("k", 1); return c.Text(200, "ok") })
			app.Handle("GET", "/get", func(c *aemeath.Ctx) error {
				return c.Text(200, session.From(c).String("k")+"|"+itoa(session.From(c).Int("k")))
			})
			w := httptest.NewRecorder()
			app.ServeHTTP(w, httptest.NewRequest("GET", "https://example.com/set", nil))
			ck := sessionCookie(w, tc.want)
			if ck == nil {
				t.Fatalf("no cookie named %q; Set-Cookie: %q", tc.want, w.Header().Values("Set-Cookie"))
			}
			if strings.HasPrefix(ck.Name, "__Host-") && (!ck.Secure || ck.Path != "/" || ck.Domain != "") {
				t.Errorf("a __Host- cookie must be Secure, Path=/ and without Domain: %+v", ck)
			}
			// The app reads back the cookie it wrote.
			r := httptest.NewRequest("GET", "https://example.com/get", nil)
			r.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
			w = httptest.NewRecorder()
			app.ServeHTTP(w, r)
			if w.Body.String() != "|1" {
				t.Errorf("next request read %q, want the stored value", w.Body.String())
			}
		})
	}
}

// A cookie named with a __Host- or __Secure- prefix that the browser would
// refuse is a configuration mistake: every login would silently fail.
func TestCookiePrefixRulesAreCheckedAtStartup(t *testing.T) {
	for name, cfg := range map[string]session.Config{
		"__Host- over plain http": {CookieName: "__Host-sid", Insecure: true},
		"__Host- with Domain":     {CookieName: "__Host-sid", Domain: "example.com"},
		"__Host- with a Path":     {CookieName: "__Host-sid", Path: "/app"},
		"__Secure- over http":     {CookieName: "__Secure-sid", Insecure: true},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil || !strings.Contains(r.(string), "session: Config.CookieName") {
					t.Errorf("panic = %v, want one naming Config.CookieName", r)
				}
			}()
			cfg.Store = session.NewMemoryStore()
			session.Middleware(cfg)
		})
	}
	// Allowed: __Host- that follows the rules, __Secure- over https.
	session.Middleware(session.Config{Store: session.NewMemoryStore(), CookieName: "__Host-sid"})
	session.Middleware(session.Config{Store: session.NewMemoryStore(), CookieName: "__Secure-sid", Domain: "example.com"})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}
