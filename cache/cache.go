// Package cache is the Redis plugin.
package cache

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"

	"github.com/redis/go-redis/v9"

	"github.com/naufalraism/aemeath"
)

// Plugin is a Redis-backed cache.
type Plugin struct {
	Client *redis.Client
}

// Config describes the Redis server.
type Config struct {
	// URL is the form managed services hand out:
	// "redis://user:password@host:6379/0", or "rediss://..." for TLS. When
	// set, Addr, Password and DB are taken from it.
	URL string

	Addr     string // host:port; "localhost:6379" when empty
	Password string
	DB       int

	// TLS connects over TLS, which managed Redis services usually require.
	// With a rediss:// URL it replaces the default settings (for example to
	// trust a private certificate authority).
	TLS *tls.Config
}

// New creates a Plugin. The connection is established lazily. It panics on
// a malformed URL, so a bad setting stops the server at startup.
//
//	cache.New(cache.Config{URL: os.Getenv("REDIS_URL")})
//	cache.New(cache.Config{Addr: "localhost:6379"})
func New(cfg Config) *Plugin {
	opts := &redis.Options{Addr: cfg.Addr, Password: cfg.Password, DB: cfg.DB}
	if cfg.URL != "" {
		var err error
		if opts, err = redis.ParseURL(cfg.URL); err != nil {
			panic(fmt.Sprintf("cache: Config.URL: %v", err))
		}
	}
	if cfg.TLS != nil {
		t := cfg.TLS.Clone()
		if t.ServerName == "" {
			host, _, _ := net.SplitHostPort(opts.Addr)
			t.ServerName = host
		}
		opts.TLSConfig = t
	}
	return &Plugin{Client: redis.NewClient(opts)}
}

// Apply provides the *redis.Client to handlers, adds a "cache" readiness check
// (see App.EnableHealth) and closes the connection when the server shuts down.
func (p *Plugin) Apply(app *aemeath.App) error {
	aemeath.Provide(app, p.Client)
	app.AddReadyCheck("cache", func(ctx context.Context) error { return p.Client.Ping(ctx).Err() })
	app.OnShutdown(func(context.Context) error { return p.Close() })
	return nil
}

// From returns the Redis client of the application handling c.
func From(c *aemeath.Ctx) *redis.Client { return aemeath.MustGet[*redis.Client](c) }

// Close closes the Redis connection; call it when the server shuts down.
func (p *Plugin) Close() error { return p.Client.Close() }
