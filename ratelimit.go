package aemeath

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type visitor struct {
	lastSeen time.Time
	count    int
}

type rateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	limit    int
	window   time.Duration
}

func RateLimiter(limit int, window time.Duration) Middleware {
	rl := &rateLimiter{
		visitors: make(map[string]*visitor),
		limit:    limit,
		window:   window,
	}

	go func() {
		for {
			time.Sleep(window)
			rl.mu.Lock()
			for ip, v := range rl.visitors {
				if time.Since(v.lastSeen) > window {
					delete(rl.visitors, ip)
				}
			}
			rl.mu.Unlock()
		}
	}()

	return func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) error {
			// PERBAIKAN: Ambil IP saja, buang nomor port acak
			ip, _, err := net.SplitHostPort(c.R.RemoteAddr)
			if err != nil {
				ip = c.R.RemoteAddr // Fallback jika gagal
			}

			rl.mu.Lock()
			v, exists := rl.visitors[ip]

			if !exists || time.Since(v.lastSeen) > rl.window {
				rl.visitors[ip] = &visitor{lastSeen: time.Now(), count: 1}
				rl.mu.Unlock()
				return next(c)
			}

			if v.count >= rl.limit {
				rl.mu.Unlock()
				return NewError(http.StatusTooManyRequests, "Terlalu banyak permintaan. Silakan coba lagi nanti.")
			}

			v.count++
			v.lastSeen = time.Now()
			rl.mu.Unlock()

			return next(c)
		}
	}
}
