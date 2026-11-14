package session

import (
	"context"
	"errors"
	"fmt"
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
