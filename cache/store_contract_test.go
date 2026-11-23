package cache_test

import (
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/naufalraism/aemeath/cache"
	"github.com/naufalraism/aemeath/internal/storetest"
)

func TestRedisSessionStoreContract(t *testing.T) {
	storetest.Run(t, func(t *testing.T) storetest.Store {
		mr := miniredis.RunT(t)
		p := cache.New(mr.Addr(), "", 0)
		t.Cleanup(func() { _ = p.Close() })
		return storetest.Store{Store: cache.NewSessionStore(p, ""), Advance: mr.FastForward}
	})
}
