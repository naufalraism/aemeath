package aemeath

import (
	"net"
	"strconv"
	"sync"
	"time"
)

type visitor struct {
	windowStart time.Time
	count       int
}

type rateLimiter struct {
	mu        sync.Mutex
	visitors  map[string]*visitor
	limit     int
	window    time.Duration
	lastSweep time.Time
}

// allow records a request for key and reports whether it is within the limit.
// When it is not, the returned duration says how long until the window resets.
func (rl *rateLimiter) allow(key string, now time.Time) (bool, time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Drop expired entries at most once per window; no background goroutine needed.
	if now.Sub(rl.lastSweep) >= rl.window {
		for k, v := range rl.visitors {
			if now.Sub(v.windowStart) >= rl.window {
				delete(rl.visitors, k)
			}
		}
		rl.lastSweep = now
	}

	v, ok := rl.visitors[key]
	if !ok || now.Sub(v.windowStart) >= rl.window {
		rl.visitors[key] = &visitor{windowStart: now, count: 1}
		return true, 0
	}
	if v.count >= rl.limit {
		return false, rl.window - now.Sub(v.windowStart)
	}
	v.count++
	return true, 0
}

// RateLimiter limits each client IP to `limit` requests per `window`
// (fixed window, anchored at the client's first request in the window).
//
// The client IP is taken from the connection (RemoteAddr). Behind a reverse
// proxy every request shares the proxy's address, so terminate the limit at
// the proxy or restore the real client address before this middleware runs.
func RateLimiter(limit int, window time.Duration) Middleware {
	if limit <= 0 || window <= 0 {
		panic("aemeath: RateLimiter requires limit > 0 and window > 0")
	}
	rl := &rateLimiter{
		visitors: make(map[string]*visitor),
		limit:    limit,
		window:   window,
	}

	return func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) error {
			// Use the IP only; the port changes on every connection.
			ip, _, err := net.SplitHostPort(c.R.RemoteAddr)
			if err != nil {
				ip = c.R.RemoteAddr
			}

			ok, retry := rl.allow(ip, time.Now())
			if !ok {
				secs := int((retry + time.Second - 1) / time.Second)
				c.W.Header().Set("Retry-After", strconv.Itoa(secs))
				return TooManyRequests("too many requests, please try again later")
			}
			return next(c)
		}
	}
}
