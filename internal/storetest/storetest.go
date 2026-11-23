// Package storetest is one set of tests that every session.Store must pass:
// session.MemoryStore and cache.SessionStore (Redis) both run it, so the two
// behave the same for the session middleware.
package storetest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/naufalraism/aemeath/session"
)

// Store is a store under test and a way to move its clock forward.
type Store struct {
	session.Store
	Advance func(time.Duration)
}

// Run runs the contract against fresh stores made by newStore.
func Run(t *testing.T, newStore func(t *testing.T) Store) {
	ctx := context.Background()
	data := func() map[string]any {
		return map[string]any{
			"user": "ann", "admin": true, "n": 7, "ratio": 0.5,
			"tags": []any{"a", "b"}, "prefs": map[string]any{"theme": "dark", "sizes": []any{1, 2}},
		}
	}

	t.Run("missing id", func(t *testing.T) {
		s := newStore(t)
		got, ok, err := s.Load(ctx, "nope")
		if err != nil || ok || got != nil {
			t.Fatalf("Load(missing) = %v, %v, %v; want nil, false, nil", got, ok, err)
		}
	})

	t.Run("save then load", func(t *testing.T) {
		s := newStore(t)
		if err := s.Save(ctx, "a", data(), time.Hour); err != nil {
			t.Fatal(err)
		}
		got, ok, err := s.Load(ctx, "a")
		if err != nil || !ok {
			t.Fatalf("Load = %v, %v", ok, err)
		}
		sameJSON(t, got, data())
	})

	t.Run("save replaces the whole session", func(t *testing.T) {
		s := newStore(t)
		_ = s.Save(ctx, "a", data(), time.Hour)
		if err := s.Save(ctx, "a", map[string]any{"user": "bob"}, time.Hour); err != nil {
			t.Fatal(err)
		}
		got, _, _ := s.Load(ctx, "a")
		sameJSON(t, got, map[string]any{"user": "bob"})
	})

	t.Run("empty session", func(t *testing.T) {
		s := newStore(t)
		if err := s.Save(ctx, "a", map[string]any{}, time.Hour); err != nil {
			t.Fatal(err)
		}
		got, ok, err := s.Load(ctx, "a")
		if err != nil || !ok || len(got) != 0 {
			t.Fatalf("Load(empty) = %v, %v, %v; want an empty map, true, nil", got, ok, err)
		}
	})

	t.Run("delete", func(t *testing.T) {
		s := newStore(t)
		_ = s.Save(ctx, "a", data(), time.Hour)
		if err := s.Delete(ctx, "a"); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := s.Load(ctx, "a"); ok || err != nil {
			t.Fatalf("Load after Delete = %v, %v; want false, nil", ok, err)
		}
		if err := s.Delete(ctx, "never-saved"); err != nil {
			t.Fatalf("Delete(missing) = %v, want nil", err)
		}
	})

	t.Run("expiry", func(t *testing.T) {
		s := newStore(t)
		_ = s.Save(ctx, "a", data(), time.Minute)
		s.Advance(59 * time.Second)
		if _, ok, _ := s.Load(ctx, "a"); !ok {
			t.Fatal("the session expired before its TTL")
		}
		s.Advance(2 * time.Second)
		if got, ok, err := s.Load(ctx, "a"); ok || err != nil {
			t.Fatalf("Load after the TTL = %v, %v, %v; want gone", got, ok, err)
		}
	})

	t.Run("saving again restarts the TTL", func(t *testing.T) {
		s := newStore(t)
		_ = s.Save(ctx, "a", data(), time.Minute)
		s.Advance(50 * time.Second)
		_ = s.Save(ctx, "a", data(), time.Minute)
		s.Advance(50 * time.Second)
		if _, ok, _ := s.Load(ctx, "a"); !ok {
			t.Fatal("the session expired although it was saved again 50 s ago with a 1 min TTL")
		}
	})

	t.Run("callers cannot change what is stored", func(t *testing.T) {
		s := newStore(t)
		d := data()
		_ = s.Save(ctx, "a", d, time.Hour)
		d["user"] = "mallory"
		d["prefs"].(map[string]any)["theme"] = "changed after Save"
		got, _, _ := s.Load(ctx, "a")
		got["user"] = "changed after Load"
		got["prefs"].(map[string]any)["theme"] = "changed after Load"
		again, _, _ := s.Load(ctx, "a")
		sameJSON(t, again, data())
	})

	t.Run("concurrent use", func(t *testing.T) {
		s := newStore(t)
		var wg sync.WaitGroup
		errs := make(chan error, 200)
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				id := fmt.Sprintf("id-%d", i%10)
				if err := s.Save(ctx, id, map[string]any{"i": i, "nested": map[string]any{"i": i}}, time.Hour); err != nil {
					errs <- err
				}
				if _, _, err := s.Load(ctx, id); err != nil {
					errs <- err
				}
				if i%7 == 0 {
					if err := s.Delete(ctx, id); err != nil {
						errs <- err
					}
				}
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
	})
}

// sameJSON compares two sessions the way they reach the application: as
// JSON values (a store may return numbers as float64, see session.Int).
func sameJSON(t *testing.T, got, want map[string]any) {
	t.Helper()
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Fatalf("loaded %s, want %s", g, w)
	}
}
