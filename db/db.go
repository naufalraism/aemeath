// Package db is the GORM (SQLite) plugin.
package db

import (
	"context"
	"fmt"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/naufalraism/aemeath"
)

// Plugin holds the database connection.
type Plugin struct {
	DB *gorm.DB
}

// Open opens a SQLite database and auto-migrates the given models.
func Open(filename string, models ...any) (*Plugin, error) {
	g, err := gorm.Open(sqlite.Open(filename), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("aemeath/db: failed to connect to database: %w", err)
	}
	if len(models) > 0 {
		if err := g.AutoMigrate(models...); err != nil {
			return nil, fmt.Errorf("aemeath/db: failed to migrate database: %w", err)
		}
	}
	return &Plugin{DB: g}, nil
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
