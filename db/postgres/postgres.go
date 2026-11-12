// Package postgres connects the database plugin to PostgreSQL.
//
//	plugin, err := postgres.Open(os.Getenv("DATABASE_URL"))
//	// DATABASE_URL=postgres://user:password@localhost:5432/app?sslmode=disable
package postgres

import (
	"time"

	pg "gorm.io/driver/postgres"

	"github.com/naufalraism/aemeath/db"
)

// Open connects to PostgreSQL. dsn is a URL ("postgres://user:pass@host:5432/name?sslmode=disable")
// or a key/value string ("host=... user=... dbname=..."). The pool defaults to 25
// open and 5 idle connections for 30 minutes; pass db.WithPool to change it.
func Open(dsn string, opts ...db.Option) (*db.Plugin, error) {
	all := append([]db.Option{db.WithPool(25, 5, 30*time.Minute)}, opts...)
	return db.New(pg.Open(dsn), all...)
}
