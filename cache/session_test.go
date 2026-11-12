package cache_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/cache"
	"github.com/naufalraism/aemeath/session"
)

type none struct{}
type out struct {
	User string `json:"user"`
	N    int    `json:"n"`
}

func redisApp(t *testing.T) (*aemeath.App, *miniredis.Miniredis, *cache.SessionStore) {
	mr := miniredis.RunT(t)
	p := cache.New(mr.Addr(), "", 0)
	t.Cleanup(func() { _ = p.Close() })
	store := cache.NewSessionStore(p, "")
	app := aemeath.New()
	app.Use(session.Middleware(session.Config{Store: store, Insecure: true, TTL: time.Hour}))
	aemeath.POST(app, "/login", func(c *aemeath.Ctx, _ none) (out, error) {
		s := session.From(c)
		if err := s.Renew(c.Context()); err != nil {
			return out{}, err
		}
		s.Set("user", "ann")
		s.Set("n", 7)
		return out{}, nil
	})
	aemeath.GET(app, "/me", func(c *aemeath.Ctx, _ none) (out, error) {
		s := session.From(c)
		return out{User: s.String("user"), N: s.Int("n")}, nil
	})
	return app, mr, store
}

func do(app *aemeath.App, method, path, sid string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(""))
	if sid != "" {
		r.AddCookie(&http.Cookie{Name: "aemeath_session", Value: sid})
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

func TestRedisSessionsRoundTripAndExpire(t *testing.T) {
	app, mr, _ := redisApp(t)
	id := sid(do(app, "POST", "/login", ""))
	if id == "" || len(mr.Keys()) != 1 {
		t.Fatalf("id=%q keys=%v", id, mr.Keys())
	}
	if got := do(app, "GET", "/me", id).Body.String(); !strings.Contains(got, `"user":"ann"`) || !strings.Contains(got, `"n":7`) {
		t.Fatalf("session data lost through JSON: %s", got)
	}
	if ttl := mr.TTL("sess:" + id); ttl <= 0 || ttl > time.Hour {
		t.Fatalf("ttl not applied: %v", ttl)
	}
	mr.FastForward(2 * time.Hour)
	if got := do(app, "GET", "/me", id).Body.String(); strings.Contains(got, "ann") {
		t.Fatal("expired session still works")
	}
}

func TestRedisRenewRemovesTheOldKey(t *testing.T) {
	app, mr, _ := redisApp(t)
	first := sid(do(app, "POST", "/login", ""))
	second := sid(do(app, "POST", "/login", first))
	if first == second || len(mr.Keys()) != 1 || mr.Exists("sess:"+first) {
		t.Fatalf("old session not removed: %v", mr.Keys())
	}
}

func TestRedisCorruptEntryIsNoSessionAndOutageIsAnError(t *testing.T) {
	app, mr, store := redisApp(t)
	id := sid(do(app, "POST", "/login", ""))
	_ = mr.Set("sess:"+id, "{not json")
	if got := do(app, "GET", "/me", id).Body.String(); strings.Contains(got, "ann") {
		t.Fatal("corrupt entry produced a session")
	}
	mr.Close()
	if _, _, err := store.Load(context.Background(), id); err == nil {
		t.Fatal("a Redis outage must be reported, not treated as an empty session")
	}
	if w := do(app, "GET", "/me", id); w.Code != 500 {
		t.Fatalf("outage should fail the request, got %d", w.Code)
	}
}
