# Plugins and typed dependencies

Plugins live in their own packages, so you only pay for what you import.

| Package | Provides |
| :--- | :--- |
| `aemeath/jwt` | Bearer tokens: shared secret, public key or JWKS (`jwt.Middleware`, `jwt.Issue`) |
| `aemeath/session`, `aemeath/csrf` | Server-side sessions and CSRF protection |
| `aemeath/oidc` | OpenID Connect login (PKCE, nonce, JWKS) |
| `aemeath/db` | GORM (`db.From`); open with `db/sqlite`, `db/postgres` or `db/mysql` |
| `aemeath/cache` | Redis (`cache.New`, `cache.From`, `cache.NewSessionStore`) |
| `aemeath/metrics`, `aemeath/tracing` | Prometheus metrics; W3C tracing exported over OTLP |
| `aemeath/mailer` | SMTP (`mailer.New`, `mailer.From`) |
| `aemeath/oauth2` | OAuth2 login for providers without OpenID Connect (`oauth2.New`, `Begin`, `Complete`) |
| `aemeath/ws` | WebSockets (`ws.Upgrade`) |

```go
plugin, err := sqlite.Open("app.db", db.WithModels(&User{}))
if err != nil { log.Fatal(err) }
if err := app.RegisterPlugin(plugin); err != nil { // also registers a readiness check for /readyz
	log.Fatal(err)
}

aemeath.GET(app, "/users", func(c *aemeath.Ctx, _ struct{}) ([]User, error) {
	var users []User
	return users, db.From(c).Find(&users).Error
})
```

## Your own services

```go
aemeath.Provide(app, mySvc)                    // at startup
svc := aemeath.MustGet[*MySvc](c)              // in a handler; a clear panic if missing
svc, ok := aemeath.Get[*MySvc](c)              // or check yourself
aemeath.Provide[Notifier](app, emailNotifier)  // name an interface explicitly
```

## Writing a plugin

```go
type Plugin struct{ /* ... */ }

func (p *Plugin) Apply(app *aemeath.App) error {
	aemeath.Provide(app, p)
	app.AddReadyCheck("mine", func(ctx context.Context) error { return p.ping(ctx) })
	return nil
}
```
