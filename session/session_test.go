package session_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/session"
)

type none struct{}
type out struct {
	User string `json:"user"`
	ID   string `json:"id"`
}

func newApp(store session.Store, sliding bool) *aemeath.App {
	app := aemeath.New()
	app.Use(session.Middleware(session.Config{Store: store, Insecure: true, Sliding: sliding, TTL: time.Hour}))
	aemeath.POST(app, "/login", func(c *aemeath.Ctx, _ none) (out, error) {
		s := session.From(c)
		if err := s.Renew(c.Context()); err != nil {
			return out{}, err
		}
		s.Set("user", "ann")
		return out{User: "ann"}, nil
	})
	aemeath.POST(app, "/set", func(c *aemeath.Ctx, _ none) (out, error) {
		session.From(c).Set("user", "ann") // no Renew on purpose
		return out{}, nil
	})
	aemeath.GET(app, "/me", func(c *aemeath.Ctx, _ none) (out, error) {
		s := session.From(c)
		return out{User: s.String("user"), ID: s.ID()}, nil
	})
	aemeath.POST(app, "/logout", func(c *aemeath.Ctx, _ none) (out, error) {
		session.From(c).Destroy()
		return out{}, nil
	})
	return app
}

func do(app *aemeath.App, method, path, cookie string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(""))
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: "aemeath_session", Value: cookie})
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}

func sid(w *httptest.ResponseRecorder) string {
	for _, c := range w.Result().Cookies() {
		if c.Name == "aemeath_session" {
			return c.Value
		}
	}
	return ""
}

func TestLoginFlowAndCookieFlags(t *testing.T) {
	app := newApp(session.NewMemoryStore(), false)
	w := do(app, "POST", "/login", "")
	id := sid(w)
	if len(id) != 43 {
		t.Fatalf("want 43-char id, got %q", id)
	}
	ck := w.Result().Cookies()[0]
	if !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie flags: %+v", ck)
	}
	if got := do(app, "GET", "/me", id).Body.String(); !strings.Contains(got, `"ann"`) {
		t.Fatalf("session not kept: %s", got)
	}
	if got := do(app, "GET", "/me", "").Body.String(); strings.Contains(got, "ann") {
		t.Fatalf("anonymous saw a user: %s", got)
	}
}

func TestUnknownIDIsNeverAdopted(t *testing.T) {
	store := session.NewMemoryStore()
	app := newApp(store, false)
	attacker := strings.Repeat("A", 43)
	w := do(app, "POST", "/login", attacker)
	if id := sid(w); id == attacker || id == "" {
		t.Fatalf("attacker-chosen id adopted or none issued: %q", id)
	}
	if got := do(app, "GET", "/me", attacker).Body.String(); strings.Contains(got, "ann") {
		t.Fatalf("attacker id gained the login: %s", got)
	}
}

func TestRenewDeletesTheOldSession(t *testing.T) {
	store := session.NewMemoryStore()
	app := newApp(store, false)
	// Pre-existing real session that then logs in again.
	first := sid(do(app, "POST", "/login", ""))
	second := sid(do(app, "POST", "/login", first))
	if second == first {
		t.Fatal("id not rotated")
	}
	if got := do(app, "GET", "/me", first).Body.String(); strings.Contains(got, "ann") {
		t.Fatalf("old id still valid after Renew: %s", got)
	}
	if store.Len() != 1 {
		t.Fatalf("want 1 live session, got %d", store.Len())
	}
}

func TestDestroyClearsCookieAndStore(t *testing.T) {
	store := session.NewMemoryStore()
	app := newApp(store, false)
	id := sid(do(app, "POST", "/login", ""))
	w := do(app, "POST", "/logout", id)
	cleared := false
	for _, c := range w.Result().Cookies() {
		if c.Name == "aemeath_session" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared || store.Len() != 0 {
		t.Fatalf("cleared=%v live=%d", cleared, store.Len())
	}
	if got := do(app, "GET", "/me", id).Body.String(); strings.Contains(got, "ann") {
		t.Fatal("destroyed session still works")
	}
}

func TestExpiryAndSliding(t *testing.T) {
	now := time.Now()
	store := session.NewMemoryStore().WithClock(func() time.Time { return now })
	app := newApp(store, true)
	id := sid(do(app, "POST", "/login", ""))
	now = now.Add(50 * time.Minute)
	if !strings.Contains(do(app, "GET", "/me", id).Body.String(), "ann") {
		t.Fatal("expired too early")
	}
	now = now.Add(50 * time.Minute) // 100m total, but sliding extended at 50m
	if !strings.Contains(do(app, "GET", "/me", id).Body.String(), "ann") {
		t.Fatal("sliding TTL not extended")
	}
	now = now.Add(2 * time.Hour)
	if strings.Contains(do(app, "GET", "/me", id).Body.String(), "ann") {
		t.Fatal("session did not expire")
	}
}

func TestNoCookieWhenSessionUntouched(t *testing.T) {
	app := newApp(session.NewMemoryStore(), false)
	if id := sid(do(app, "GET", "/me", "")); id != "" {
		t.Fatalf("cookie issued for untouched session: %q", id)
	}
}

func TestStoreContract(t *testing.T) {
	s := session.NewMemoryStore()
	ctx := context.Background()
	_ = s.Save(ctx, "x", map[string]any{"a": 1}, time.Hour)
	d, ok, _ := s.Load(ctx, "x")
	d["a"] = 2 // must not alias the stored copy
	d2, _, _ := s.Load(ctx, "x")
	if !ok || d2["a"] != 1 {
		t.Fatal("store aliases data")
	}
}

func TestUnknownIDNotAdoptedWithoutRenew(t *testing.T) {
	app := newApp(session.NewMemoryStore(), false)
	attacker := strings.Repeat("B", 43)
	w := do(app, "POST", "/set", attacker)
	if id := sid(w); id == attacker || id == "" {
		t.Fatalf("attacker-chosen id adopted: %q", id)
	}
	if got := do(app, "GET", "/me", attacker).Body.String(); strings.Contains(got, "ann") {
		t.Fatal("attacker id gained data")
	}
}
