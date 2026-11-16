package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func raw(s *MemoryStore) int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.m) }

// Abandoned sessions (never looked up again) must not stay in memory forever.
func TestMemoryStoreSweepsAbandonedSessions(t *testing.T) {
	now := time.Now()
	s := NewMemoryStore().WithClock(func() time.Time { return now })
	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		if err := s.Save(ctx, fmt.Sprintf("old-%d", i), map[string]any{"a": 1}, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	if raw(s) != 1000 {
		t.Fatalf("setup: %d", raw(s))
	}
	now = now.Add(2 * time.Hour) // all expired, nobody ever comes back
	if err := s.Save(ctx, "fresh", map[string]any{}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := raw(s); got != 1 {
		t.Fatalf("expired sessions were not swept: %d entries remain", got)
	}
}

// A flood of one-time visitors is bounded by the cap and does not evict real users.
func TestMemoryStoreCap(t *testing.T) {
	now := time.Now()
	s := NewMemoryStore().WithClock(func() time.Time { return now }).WithMaxSessions(100)
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		if err := s.Save(ctx, fmt.Sprintf("u-%d", i), nil, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5000; i++ {
		err := s.Save(ctx, fmt.Sprintf("flood-%d", i), nil, time.Hour)
		if !errors.Is(err, ErrStoreFull) {
			t.Fatalf("flood %d: want ErrStoreFull, got %v", i, err)
		}
	}
	if raw(s) != 100 {
		t.Fatalf("cap exceeded: %d", raw(s))
	}
	// An existing user can still update their session while the store is full.
	if err := s.Save(ctx, "u-0", map[string]any{"x": 1}, time.Hour); err != nil {
		t.Fatalf("existing session refused: %v", err)
	}
	// Once old sessions expire, room is made again (sweep when full).
	now = now.Add(2 * time.Hour)
	if err := s.Save(ctx, "new", nil, time.Hour); err != nil {
		t.Fatalf("no room after expiry: %v", err)
	}
	if raw(s) != 1 {
		t.Fatalf("after expiry: %d", raw(s))
	}
}

func TestMemoryStoreDefaultCap(t *testing.T) {
	if NewMemoryStore().max != DefaultMaxSessions || NewMemoryStore().WithMaxSessions(0).max != DefaultMaxSessions {
		t.Fatal("default cap not applied")
	}
}

// A map or slice stored in a session used to be shared by every request of
// that session (the copy was one level deep); two requests writing to it at
// the same time crashed the program with "concurrent map writes".
func TestMemoryStoreCopiesNestedValues(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	orig := map[string]any{
		"cart":  map[string]int{"a": 1},
		"tags":  []string{"x"},
		"deep":  map[string]any{"list": []any{map[string]any{"n": 1}}},
		"count": 3,
	}
	if err := s.Save(ctx, "id", orig, time.Minute); err != nil {
		t.Fatal(err)
	}
	orig["cart"].(map[string]int)["a"] = 99 // the caller's value is not the stored one

	a, _, _ := s.Load(ctx, "id")
	b, _, _ := s.Load(ctx, "id")
	if a["cart"].(map[string]int)["a"] != 1 {
		t.Fatal("Save kept a reference to the caller's map")
	}
	var wg sync.WaitGroup
	for _, d := range []map[string]any{a, b} { // the race detector checks this
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				d["cart"].(map[string]int)["a"]++
				d["tags"].([]string)[0] = "y"
				d["deep"].(map[string]any)["list"].([]any)[0].(map[string]any)["n"] = i
			}
		}()
	}
	wg.Wait()
	c, _, _ := s.Load(ctx, "id")
	if c["cart"].(map[string]int)["a"] != 1 || c["tags"].([]string)[0] != "x" || c["count"] != 3 {
		t.Fatalf("a loaded copy changed the stored session: %v", c)
	}
}
