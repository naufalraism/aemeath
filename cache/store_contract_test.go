package cache_test

import (
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/naufalraism/aemeath/cache"
	"github.com/naufalraism/aemeath/internal/storetest"
	"github.com/naufalraism/aemeath/session"
)

func TestRedisSessionStoreContract(t *testing.T) {
	storetest.Run(t, func(t *testing.T) storetest.Store {
		mr := miniredis.RunT(t)
		p := cache.New(cache.Config{Addr: mr.Addr()})
		t.Cleanup(func() { _ = p.Close() })
		return storetest.Store{Store: cache.NewSessionStore(p, ""), Advance: mr.FastForward}
	})
}

func TestRedisSessionStoreConcurrentRequests(t *testing.T) {
	storetest.RunConcurrentRequests(t, func(t *testing.T) session.Store {
		mr := miniredis.RunT(t)
		p := cache.New(cache.Config{Addr: mr.Addr()})
		t.Cleanup(func() { _ = p.Close() })
		return cache.NewSessionStore(p, "")
	})
}

func TestRedisSessionStoreValueTypes(t *testing.T) {
	storetest.RunValueTypes(t, func(t *testing.T) session.Store {
		mr := miniredis.RunT(t)
		p := cache.New(cache.Config{Addr: mr.Addr()})
		t.Cleanup(func() { _ = p.Close() })
		return cache.NewSessionStore(p, "")
	})
}
