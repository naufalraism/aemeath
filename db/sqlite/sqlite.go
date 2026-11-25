// Package sqlite connects the database plugin to a SQLite file. The driver
// is written in Go: no C compiler is needed.
//
//	plugin, err := sqlite.Open("app.db", db.WithModels(&User{}))
package sqlite

import (
	gsqlite "github.com/glebarez/sqlite"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/db"
)

// Open opens the SQLite database file filename, creating it if needed. Pass
// db.WithModels to auto-migrate your models. While the program only writes
// its TypeScript files (see aemeath.GeneratingTypeScript), the file is not
// opened, created or migrated.
func Open(filename string, opts ...db.Option) (*db.Plugin, error) {
	if aemeath.GeneratingTypeScript() {
		filename = ":memory:"
	}
	return db.New(gsqlite.Open(filename), opts...)
}
