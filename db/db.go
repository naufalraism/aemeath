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

// Apply provides the *gorm.DB to handlers and adds a "database" readiness
// check (see App.EnableHealth).
func (p *Plugin) Apply(app *aemeath.App) error {
	aemeath.Provide(app, p.DB)
	app.AddReadyCheck("database", func(ctx context.Context) error {
		sqlDB, err := p.DB.DB()
		if err != nil {
			return err
		}
		return sqlDB.PingContext(ctx)
	})
	return nil
}

// From returns the database of the application handling c. It panics with a
// clear message if the plugin was not registered.
func From(c *aemeath.Ctx) *gorm.DB { return aemeath.MustGet[*gorm.DB](c) }
