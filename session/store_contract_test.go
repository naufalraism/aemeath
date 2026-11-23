package session_test

import (
	"testing"
	"time"

	"github.com/naufalraism/aemeath/internal/storetest"
	"github.com/naufalraism/aemeath/session"
)

func TestMemoryStoreContract(t *testing.T) {
	storetest.Run(t, func(t *testing.T) storetest.Store {
		now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		m := session.NewMemoryStore().WithClock(func() time.Time { return now })
		return storetest.Store{Store: m, Advance: func(d time.Duration) { now = now.Add(d) }}
	})
}
