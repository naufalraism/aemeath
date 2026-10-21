package aemeath

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// CachePlugin adalah plugin untuk caching menggunakan Redis.
type CachePlugin struct {
	Client *redis.Client
}

// NewCache membuat instance CachePlugin baru.
func NewCache(addr string, password string, db int) *CachePlugin {
	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	return &CachePlugin{Client: rdb}
}

// Name memenuhi antarmuka Plugin.
func (p *CachePlugin) Name() string { return "cache" }

// Apply menyuntikkan Redis client ke dalam Context.
func (p *CachePlugin) Apply(app *App) error {
	app.Use(func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) error {
			c.State["cache"] = p.Client
			return next(c)
		}
	})
	return nil
}

// Helper: Set menyimpan data ke cache dengan masa berlaku (TTL).
func (p *CachePlugin) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	return p.Client.Set(ctx, key, value, ttl).Err()
}

// Helper: Get mengambil data dari cache.
func (p *CachePlugin) Get(ctx context.Context, key string) (string, error) {
	return p.Client.Get(ctx, key).Result()
}
