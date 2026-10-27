// Package cache is the Redis plugin.
package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/naufalraism/aemeath"
)

// Plugin is a Redis-backed cache.
type Plugin struct {
	Client *redis.Client
}

// New creates a Plugin. The connection is established lazily.
func New(addr, password string, db int) *Plugin {
	return &Plugin{Client: redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db})}
}

// Name implements aemeath.Plugin.
func (p *Plugin) Name() string { return "cache" }

// Apply provides the *redis.Client to handlers and adds a "cache" readiness
// check (see App.EnableHealth).
func (p *Plugin) Apply(app *aemeath.App) error {
	aemeath.Provide(app, p.Client)
	app.AddReadyCheck("cache", func(ctx context.Context) error { return p.Client.Ping(ctx).Err() })
	return nil
}

// From returns the Redis client of the application handling c.
func From(c *aemeath.Ctx) *redis.Client { return aemeath.MustGet[*redis.Client](c) }

// Set stores a value with a time-to-live (TTL).
func (p *Plugin) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return p.Client.Set(ctx, key, value, ttl).Err()
}

// Get reads a value. A missing key returns redis.Nil.
func (p *Plugin) Get(ctx context.Context, key string) (string, error) {
	return p.Client.Get(ctx, key).Result()
}

// Close closes the Redis connection; call it when the server shuts down.
func (p *Plugin) Close() error { return p.Client.Close() }
