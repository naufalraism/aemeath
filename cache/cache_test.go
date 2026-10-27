package cache_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/cache"
)

func TestSetGetAndExpiry(t *testing.T) {
	mr := miniredis.RunT(t)
	p := cache.New(mr.Addr(), "", 0)
	defer func() { _ = p.Close() }()
	ctx := context.Background()

	if err := p.Set(ctx, "k", "v", time.Minute); err != nil {
		t.Fatal(err)
	}
	if got, err := p.Get(ctx, "k"); err != nil || got != "v" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if ttl := mr.TTL("k"); ttl != time.Minute {
		t.Fatalf("ttl = %v, want 1m", ttl)
	}
	mr.FastForward(2 * time.Minute)
	if _, err := p.Get(ctx, "k"); !errors.Is(err, redis.Nil) {
		t.Fatalf("expired key: err = %v, want redis.Nil", err)
	}
}

func TestPluginProvidesTheClientAndReadiness(t *testing.T) {
	mr := miniredis.RunT(t)
	p := cache.New(mr.Addr(), "", 0)
	defer func() { _ = p.Close() }()

	app := aemeath.New()
	app.EnableHealth()
	if err := app.RegisterPlugin(p); err != nil {
		t.Fatal(err)
	}
	app.Handle("GET", "/hits", func(c *aemeath.Ctx) error {
		n, err := cache.From(c).Incr(c.Context(), "hits").Result()
		if err != nil {
			return err
		}
		return c.JSON(200, map[string]int64{"hits": n})
	})

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	get("/hits")
	if rec := get("/hits"); !strings.Contains(rec.Body.String(), `"hits":2`) {
		t.Fatalf("body = %s", rec.Body)
	}
	if rec := get("/readyz"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"cache":"ok"`) {
		t.Fatalf("readyz = %d %s", rec.Code, rec.Body)
	}
	mr.Close()
	if rec := get("/readyz"); rec.Code != 503 || !strings.Contains(rec.Body.String(), `"cache":"failed"`) {
		t.Fatalf("readyz with Redis down = %d %s", rec.Code, rec.Body)
	}
}
