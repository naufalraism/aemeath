# Databases

`aemeath/db` wraps [GORM](https://gorm.io). SQLite works out of the box; PostgreSQL and MySQL/MariaDB are separate packages so you only download the driver you use.

| Database | Package | Connect with |
| :--- | :--- | :--- |
| SQLite | `aemeath/db` | `db.Open("app.db", &User{})` |
| PostgreSQL | `aemeath/db/postgres` | `postgres.Open(dsn, db.WithModels(&User{}))` |
| MySQL / MariaDB | `aemeath/db/mysql` | `mysql.Open(dsn, db.WithModels(&User{}))` |

```go
import (
	"github.com/naufalraism/aemeath/db"
	"github.com/naufalraism/aemeath/db/postgres"
)

plugin, err := postgres.Open(os.Getenv("DATABASE_URL"), db.WithModels(&User{}))
// DATABASE_URL=postgres://user:password@localhost:5432/app?sslmode=disable
if err != nil {
	log.Fatal(err)
}
app.RegisterPlugin(plugin)
```

Registering the plugin does three things: handlers can call `db.From(c)`, `/readyz` pings the database (see `EnableHealth`), and the connection pool is closed on graceful shutdown.

## DSN formats

```text
postgres://user:password@localhost:5432/app?sslmode=disable
user:password@tcp(localhost:3306)/app          # MySQL; parseTime=true is added for you
```

Use `sslmode=require` (or stricter) for PostgreSQL and `tls=true` for MySQL when the database is not on the same machine.

## What you get by default

- **Connection pool** of 25 open and 5 idle connections, recycled every 30 minutes. Change it with `db.WithPool(maxOpen, maxIdle, maxLifetime)`.
- **Real error types.** GORM runs with `TranslateError`, so a duplicate key is `gorm.ErrDuplicatedKey` on every database. `db.Translate(err, "user")` turns the common ones into HTTP errors: not found becomes 404, a duplicate becomes 409, and anything else is passed through (and logged as a 500 by the server).
- **MySQL times** are scanned into `time.Time` (`parseTime` is forced on).

```go
aemeath.POST(app, "/users", func(c *aemeath.Ctx, in CreateUser) (User, error) {
	u := User{Name: in.Name, Email: in.Email}
	if err := db.From(c).Create(&u).Error; err != nil {
		return User{}, db.Translate(err, "user") // duplicate email -> 409
	}
	return u, nil
})
```

## Migrations

`db.WithModels` runs GORM `AutoMigrate` at startup. That is convenient for development and small apps. For a production schema that changes over time, prefer reviewed SQL migrations (for example `golang-migrate` or `atlas`) and leave `WithModels` off.

## Tests against a real database

The package ships a shared test suite that runs against SQLite, PostgreSQL and MySQL. The PostgreSQL and MySQL tests are skipped unless these variables are set:

```bash
AEMEATH_TEST_POSTGRES_DSN='postgres://postgres:postgres@localhost:5432/test?sslmode=disable' \
AEMEATH_TEST_MYSQL_DSN='root:root@tcp(localhost:3306)/test' \
go test ./db/...
```
