// Package mysql connects the database plugin to MySQL and MariaDB.
//
//	plugin, err := mysql.Open(os.Getenv("DATABASE_URL"))
//	// DATABASE_URL=user:password@tcp(localhost:3306)/app
package mysql

import (
	"fmt"
	"time"

	driver "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"

	"github.com/naufalraism/aemeath/db"
)

// Open connects to MySQL or MariaDB. dsn is the go-sql-driver format
// ("user:pass@tcp(host:3306)/name"). parseTime is switched on for you: without
// it, time.Time columns cannot be read, which is the most common MySQL mistake.
// The pool defaults to 25 open and 5 idle connections for 30 minutes; pass
// db.WithPool to change it.
func Open(dsn string, opts ...db.Option) (*db.Plugin, error) {
	cfg, err := driver.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("aemeath/db/mysql: invalid DSN: %w", err) // the error never contains the password
	}
	cfg.ParseTime = true
	all := append([]db.Option{db.WithPool(25, 5, 30*time.Minute)}, opts...)
	return db.New(gormmysql.Open(cfg.FormatDSN()), all...)
}
