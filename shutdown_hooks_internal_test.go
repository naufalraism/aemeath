package aemeath

import (
	"context"
	"testing"
	"time"
)

// All hooks used to share one 10 second limit: a slow hook (jobs draining)
// left the next ones (tracing flush, database close) with an expired context.
func TestEachShutdownHookGetsItsOwnTime(t *testing.T) {
	old := shutdownHookTimeout
	shutdownHookTimeout = 200 * time.Millisecond
	defer func() { shutdownHookTimeout = old }()

	a := New()
	var lastErr error
	var lastLeft time.Duration
	a.OnShutdown(func(ctx context.Context) error { // registered first: runs last
		lastErr = ctx.Err()
		if dl, ok := ctx.Deadline(); ok {
			lastLeft = time.Until(dl)
		}
		return nil
	})
	a.OnShutdown(func(ctx context.Context) error { // runs first and uses all its time
		<-ctx.Done()
		return ctx.Err()
	})
	a.runShutdownHooks()
	if lastErr != nil || lastLeft < 100*time.Millisecond {
		t.Fatalf("the hook after a slow one started with ctx.Err()=%v and %v left; want a fresh limit", lastErr, lastLeft)
	}
}

func TestStopIsIdempotentAndClosesStopping(t *testing.T) {
	a := New()
	select {
	case <-a.Stopping():
		t.Fatal("Stopping is closed before Stop")
	default:
	}
	a.Stop()
	a.Stop()
	select {
	case <-a.Stopping():
	default:
		t.Fatal("Stopping is not closed after Stop")
	}
}
