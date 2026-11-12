// Package db is the GORM plugin. Open uses SQLite; the postgres and mysql
// sub-packages connect to those servers. All of them return a *Plugin, so the
// rest of your code does not depend on the database you picked.
package db

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/naufalraism/aemeath"
)

// Plugin holds the database connection.
type Plugin struct {
	DB *gorm.DB
}

type options struct {
	models  []any
	pool    *poolConfig
	gormCfg *gorm.Config
}

type poolConfig struct {
	maxOpen, maxIdle int
	maxLifetime      time.Duration
}

// Option configures New.
type Option func(*options)

// WithModels auto-migrates the given models after connecting.
func WithModels(models ...any) Option {
	return func(o *options) { o.models = append(o.models, models...) }
}

// WithPool sets the connection pool: the most open and idle connections, and how
// long a connection may be reused. Server databases get 25, 5 and 30 minutes
// unless you set it, because database/sql would otherwise open connections
// without limit.
func WithPool(maxOpen, maxIdle int, maxLifetime time.Duration) Option {
	return func(o *options) { o.pool = &poolConfig{maxOpen, maxIdle, maxLifetime} }
}

// WithConfig replaces the GORM configuration. The default is
// &gorm.Config{TranslateError: true}, which makes a duplicate key look the same
// in every database (gorm.ErrDuplicatedKey, see Translate); keep it unless you
// have a reason not to.
func WithConfig(cfg *gorm.Config) Option {
	return func(o *options) { o.gormCfg = cfg }
}

// New connects with any GORM dialector. It is what Open and the postgres and
// mysql packages are built on.
func New(d gorm.Dialector, opts ...Option) (*Plugin, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	cfg := o.gormCfg
	if cfg == nil {
		cfg = &gorm.Config{
			TranslateError: true,
			// A missing row is a normal 404, not something to log in red on every request.
			Logger: logger.New(log.New(os.Stderr, "\r\n", log.LstdFlags), logger.Config{
				SlowThreshold:             200 * time.Millisecond,
				LogLevel:                  logger.Warn,
				IgnoreRecordNotFoundError: true,
			}),
		}
	}
	g, err := gorm.Open(d, cfg)
	if err != nil {
		return nil, fmt.Errorf("aemeath/db: failed to connect to database: %w", err)
	}
	if o.pool != nil {
		sqlDB, err := g.DB()
		if err != nil {
			return nil, fmt.Errorf("aemeath/db: %w", err)
		}
		sqlDB.SetMaxOpenConns(o.pool.maxOpen)
		sqlDB.SetMaxIdleConns(o.pool.maxIdle)
		sqlDB.SetConnMaxLifetime(o.pool.maxLifetime)
	}
	p := &Plugin{DB: g}
	if len(o.models) > 0 {
		if err := g.AutoMigrate(o.models...); err != nil {
			_ = p.Close()
			return nil, fmt.Errorf("aemeath/db: failed to migrate database: %w", err)
		}
	}
	return p, nil
}

// Open opens a SQLite database file and auto-migrates the given models.
func Open(filename string, models ...any) (*Plugin, error) {
	return New(sqlite.Open(filename), WithModels(models...))
}

// Translate turns the errors every database reports differently into HTTP
// errors: no row becomes 404 ("<what> not found"), a duplicate key becomes 409
// ("<what> already exists"). Any other error is returned unchanged (and becomes
// a generic 500 that is only logged).
func Translate(err error, what string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return aemeath.NotFound(what + " not found")
	case errors.Is(err, gorm.ErrDuplicatedKey):
		return aemeath.Conflict(what + " already exists")
	}
	return err
}

// Name implements aemeath.Plugin.
func (p *Plugin) Name() string { return "database" }

// Close closes the database connection. It is safe to call more than once. The
// plugin calls it when the server shuts down; call it yourself in tests, since
// Windows cannot delete a database file that is still open.
func (p *Plugin) Close() error {
	sqlDB, err := p.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// Apply provides the *gorm.DB to handlers, adds a "database" readiness check
// (see App.EnableHealth) and closes the connection when the server shuts down.
func (p *Plugin) Apply(app *aemeath.App) error {
	aemeath.Provide(app, p.DB)
	app.AddReadyCheck("database", func(ctx context.Context) error {
		sqlDB, err := p.DB.DB()
		if err != nil {
			return err
		}
		return sqlDB.PingContext(ctx)
	})
	app.OnShutdown(func(context.Context) error { return p.Close() })
	return nil
}

// From returns the database of the application handling c. It panics with a
// clear message if the plugin was not registered.
func From(c *aemeath.Ctx) *gorm.DB { return aemeath.MustGet[*gorm.DB](c) }
