<p align="center">
  <img src="docs/public/logo.svg" width="96" alt="Aemeath logo">
</p>

# Aemeath 🚀

[![Go](https://github.com/naufalraism/aemeath/actions/workflows/go.yml/badge.svg)](https://github.com/naufalraism/aemeath/actions/workflows/go.yml)
[![golangci-lint](https://github.com/naufalraism/aemeath/actions/workflows/golangci-lint.yml/badge.svg)](https://github.com/naufalraism/aemeath/actions/workflows/golangci-lint.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/naufalraism/aemeath.svg)](https://pkg.go.dev/github.com/naufalraism/aemeath)
[![GitHub release](https://img.shields.io/github/v/release/naufalraism/aemeath?sort=semver&display_name=tag&cacheSeconds=3600)](https://github.com/naufalraism/aemeath/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**Write your API once in Go. Get the docs, the TypeScript client, the Zod schemas and the React hooks for free.**

🌐 [Website](https://aemeath-go.vercel.app) · 📚 [Documentation](https://naufalraism.github.io/aemeath/) · 🛠️ [CLI](https://github.com/naufalraism/aemeath-cli)

Aemeath is a Go web framework built on the standard `net/http` (Go 1.22+ `ServeMux` patterns). You describe each route with a plain input struct and an output struct. From those same structs Aemeath derives request binding, validation, OpenAPI, a typed TypeScript client, Zod schemas and TanStack Query hooks, and they all follow the **same rules the server enforces**.

> **Status:** pre-1.0. The API may still change between minor versions. See the [changelog](CHANGELOG.md).
> The core package has **no third-party dependencies**; plugins live in their own packages (see [Packages](#-packages)).

## ✨ Features

- **Type-safe routes**: define request/response structs once; binding and validation are automatic.
- **Rich validation**: `required`, `min`, `max`, `len`, `gt/gte/lt/lte`, `oneof`, `email`, `url`, `uuid`, `pattern`. Every failure (up to 100 per request) is reported at once as a `422` with per-field errors.
- **One source of truth**: the same tags drive the server, the OpenAPI document (`$ref` components, constraints, security, error schemas), the TypeScript client, the Zod schemas and the React Query hooks.
- **Binding** from path, query (including repeated keys), headers, cookies, JSON, and **forms and file uploads** (`form:` tag, type detected from the file's bytes, size limits); embedded structs; `time.Time`, UUIDs and any `encoding.TextUnmarshaler`.
- **Typed dependencies**: `aemeath.Get[*gorm.DB](c)` instead of `c.State["db"].(*gorm.DB)`.
- **Operations**: request IDs, structured logging (`slog`), gzip, timeouts, `/healthz` and `/readyz`, graceful shutdown.
- **Authentication**: sessions (memory or Redis), CSRF protection, JWT with shared secret, public key or JWKS (RS256, ES256, EdDSA), and OpenID Connect login with PKCE.
- **Databases**: SQLite, PostgreSQL and MySQL/MariaDB through one plugin API.
- **Observability**: Prometheus metrics and W3C tracing exported over OTLP, with no third-party dependency.
- **Realtime**: Server-Sent Events and WebSockets.
- **Testing**: `aetest` calls your app in-process through the same typed endpoints.
- **Security by default**: strict JWT, same-origin WebSockets, no directory listings, no CORS wildcard with credentials.

---

## 📊 Performance

Aemeath adds typed binding and validation on top of `net/http` for a small fixed cost per request. Under load over real TCP that cost all but disappears.

### Under load

Requests per second as a share of plain `net/http` (higher is better), with the p99 latency. Median of three 30-second runs, 0 errors in about 21 million requests. `POST /users` validates a name (3 to 50 characters) and an email.

| Route | Connections | `net/http` | **Aemeath** | Gin |
| :--- | :--- | :--- | :--- | :--- |
| `GET /hello` | 50 | 61,122 req/s · p99 3.8 ms | **96% · p99 4.0 ms** | 99% · p99 3.8 ms |
| `GET /hello` | 200 | 69,378 req/s · p99 11.9 ms | **97% · p99 12.3 ms** | 100% · p99 11.6 ms |
| `POST /users` | 50 | 50,677 req/s · p99 4.6 ms | **99% · p99 4.7 ms** | 100% · p99 4.5 ms |
| `POST /users` | 200 | 58,854 req/s · p99 14.0 ms | **96% · p99 15.6 ms** | 95% · p99 17.5 ms |

**Stress test** (50 up to 1,600 connections, one 15-second run each): throughput stops growing at about 200 connections (around 68,000 to 71,000 req/s for `GET /hello` and 58,000 to 61,000 for `POST /users`), after which latency grows instead, for all three servers. None of them returned a single error up to 1,600 connections; memory grew to about 100 MiB (Aemeath 132 MiB on the last `POST` step, a single run). Under heavy `POST` load Aemeath's p99 stayed between `net/http` and Gin (68 ms against 61 ms and 85 ms at 1,600 connections).

Measured on 23 November 2026 with Aemeath v0.4.2, Go 1.27 on Windows, Intel i7-10750H (12 threads). The servers and the load generator share that machine, so the absolute numbers are lower than on a real server; the comparison is what matters. Reproduce with `cd benchmarks && go run ./load all` (stress: `go run ./load all -c 50,100,200,400,800,1600 -d 15s -runs 1`).

### Microbenchmark

*A single JSON route using `httptest`, no network (lower is better).*

| Framework | Time (ns/op) | Memory (B/op) | Allocations |
| :--- | :--- | :--- | :--- |
| `net/http` (baseline) | 1,126 | 1,040 B | 10 |
| **Aemeath** | **2,120** | **1,152 B** | **13** |
| Gin | 2,280 | 1,433 B | 16 |

That table is one run of `go test -bench . -benchmem -count 5 -benchtime 2s` (median of five) on an Intel i7-10750H on Windows with Go 1.27, against the release of Aemeath current at the time. It is useful for the order of magnitude and for the comparison with Gin, and for nothing more: times vary by machine and Go version, and a microbenchmark says nothing about real-world throughput, where your handlers and database dominate.

Allocation counts do not depend on the machine (they do depend on the Go version), so they are the part worth watching, and they have grown since that run. On the current code with Go 1.27, measured with the commands below:

| Route | Allocations | Memory |
| :--- | :--- | :--- |
| plain `GET`, no input | 16 | 1,528 B |
| JSON `POST`, two validated fields | 32 | 7.3 KB |

Both include the cost of building the test request. To tell `{"website": ""}` from a missing key, Aemeath reads the JSON body a second time, but only when a validated value turns out to be empty; a body whose validated values are all filled in is decoded once. Measure your own routes:

```bash
go test -run '^$' -bench . -benchmem .      # Aemeath alone
cd benchmarks && go test -bench . -benchmem -count 5 -benchtime 2s   # comparison with Gin and Fiber
cd benchmarks && go run ./load all                                    # load test over TCP: net/http, Aemeath, Gin
```

Fiber is intentionally not listed because its `app.Test` helper goes through an in-memory connection and is not comparable to calling `ServeHTTP` directly.

## 📦 Installation

Requires the Go version declared in [`go.mod`](go.mod) (currently **Go 1.27**).

```bash
go get github.com/naufalraism/aemeath
```

## ⚡ Aemeath CLI

Scaffold a project, generate CRUD handlers and tests, and run a hot-reload dev server.

```bash
go install github.com/naufalraism/aemeath-cli/cmd/aemeath@latest   # or download a binary from the CLI's Releases page

aemeath new my-app                       # a minimal API
aemeath new my-app --template db         # + SQLite and a Task resource
aemeath new my-app --template fullstack  # + a React + TypeScript app that uses the generated client
aemeath generate api Product --db        # CRUD handlers + tests, registered in main.go for you
aemeath generate job SendEmail           # a background job on the job runner
aemeath dev --ts-out web/src/api         # hot reload that also rewrites the TypeScript files
```

[View the Aemeath CLI repository](https://github.com/naufalraism/aemeath-cli)

---

## 🚀 Quick Start

```go
package main

import (
	"log"

	"github.com/naufalraism/aemeath"
)

type HelloIn struct {
	Name string `path:"name"`
	Lang string `query:"lang" validate:"oneof=en es"`
}

type HelloOut struct {
	Message string `json:"message"`
}

func main() {
	app := aemeath.New()
	app.Use(aemeath.Logger(), aemeath.Recover(), aemeath.CORS())

	aemeath.GET(app, "/hello/{name}", func(c *aemeath.Ctx, in HelloIn) (HelloOut, error) {
		greeting := "Hello"
		if in.Lang == "es" {
			greeting = "Hola"
		}
		return HelloOut{Message: greeting + ", " + in.Name}, nil
	}, aemeath.Summary("Greet a user"))

	app.EnableDocs(aemeath.Info{Title: "Hello API", Version: "1.0.0"}) // /docs, /openapi.json
	app.EnableClient()                                               // /client.ts, /schemas.ts, /hooks.ts

	if err := app.Listen(":3000"); err != nil { // graceful shutdown included
		log.Fatal(err)
	}
}
```

```bash
curl "http://localhost:3000/hello/Aemeath?lang=es"
# {"message":"Hola, Aemeath"}
curl "http://localhost:3000/hello/Aemeath?lang=fr"
# 422 {"error":"validation failed","errors":[{"field":"lang","rule":"oneof","message":"must be one of: en, es"}]}
```

---

## 🧭 Routing & Binding

| Tag | Source | Methods |
| :--- | :--- | :--- |
| `path:"name"` | URL path parameter, e.g. `/users/{id}` | all |
| `query:"name"` | query string; slices use repeated keys (`?tag=a&tag=b`) | all |
| `header:"X-Name"` | request header | all |
| `cookie:"name"` | request cookie | all |
| `json:"name"` | JSON request body | `POST`, `PUT`, `PATCH` |

- Path/query/header/cookie values override anything with the same name in the body.
- Supported parameter types: `string`, integers, floats, `bool`, pointers and slices of those (slices: query only), and any type implementing `encoding.TextUnmarshaler` (`time.Time` as RFC 3339, UUID types, ...).
- Embedded structs are flattened, exactly like `encoding/json` does, and can carry path/query/header tags (handy for shared pagination structs).
- A `json`-tagged field on a `GET`/`DELETE` route is **never bound** (no body). Aemeath logs a warning at startup; use `query:"..."`.
- Unknown paths return `404`; known paths with the wrong method return `405` with an `Allow` header. Both pass through your global middlewares.

```go
aemeath.POST(app, "/users", handler, aemeath.Status(201))        // custom success status
aemeath.DELETE(app, "/users/{id}", handler, aemeath.Status(204)) // 204 sends no body
```

### Errors

Return `aemeath.NewError(code, msg)` (or `BadRequest`, `NotFound`, `Conflict`, `Forbidden`, ...) to send `{"error": "message"}`. Any other error becomes a generic `500` and is only logged. Replace the responder with `app.OnError(func(c *aemeath.Ctx, err error) { ... })` (use `aemeath.AsHTTPError(err)` for the status).

### Responses you write yourself

Use the `Handled` output type when the handler sends the response itself:

```go
aemeath.GET(app, "/old", func(c *aemeath.Ctx, _ struct{}) (aemeath.Handled, error) {
	return aemeath.Handled{}, c.Redirect(http.StatusMovedPermanently, "/new")
})
```

`Ctx` also has `SetCookie`, `File`, `Text`, and `SSE()` for Server-Sent Events:

```go
sse, err := c.SSE()          // no overall write timeout; c.Context() ends on disconnect and at shutdown
sse.Send("tick", map[string]int{"n": 1})
```

Each `Send` must reach the client within 10 seconds (`sse.SetWriteTimeout` changes it, 0 turns it off). A client that stops reading but keeps the connection open gets an error from `Send` instead of holding the handler forever; a stream that is only quiet between events is not affected.

---

## 🛡️ Validation

```go
type CreateUserIn struct {
	Name  string   `json:"name"  validate:"required,min=3,max=50" description:"Display name" example:"Ana"`
	Email string   `json:"email" validate:"required,email"`
	Role  string   `json:"role"  validate:"oneof=admin user guest"`
	Age   *int     `json:"age"   validate:"required,gte=18,lt=120"`
	Tags  []string `json:"tags"  validate:"max=5"`
	Site  string   `json:"site"  validate:"url"`
}
```

| Rule | Applies to | Meaning |
| :--- | :--- | :--- |
| `required` | all | present and non-empty |
| `min=N`, `max=N` | string (characters), slice/map (items), number (value) | inclusive bounds |
| `len=N` | string, slice, map | exactly N |
| `gt`, `gte`, `lt`, `lte` | numbers | comparisons |
| `oneof=a b c` | string, number | one of the space separated values |
| `email`, `url`, `uuid` | string | `email`: `local@domain.tld` (non-ASCII letters allowed; `a@b` is refused). `url`: absolute http/https. `uuid`: 8-4-4-4-12 hex, any version. The generated Zod schemas and OpenAPI use the same patterns as the server |
| `pattern=REGEX` | string | regular expression (RE2); must be the last rule (it may contain commas) |

How it behaves:

- **All** failures are collected and returned together as `422`:
  `{"error":"validation failed","errors":[{"field":"age","rule":"gte","message":"must be at least 18"}]}`. Nested fields are named `address.city`, `items[1].qty`, `extra[home].city`.
- Rules other than `required` only run when a value was provided, so an optional field can still be constrained. In a **JSON body**, a key that is present is provided, even when it is empty: `{"website": ""}` fails `url` and `{"tags": []}` fails `min=1`; leave the key out (or send `null`) for "no value". In **forms and query strings** an empty value counts as not sent, because an HTML form always submits its empty inputs. The generated Zod schemas follow the JSON rule.
- For **scalars** (`int`, `bool`, `float`) with `required` you **must** use a pointer so "not sent" differs from "sent as zero". Aemeath panics at **startup** for that, for `required`+`omitempty`, and for unknown or misapplied rules (`min` on a bool, `email` on a number, a bad regex, ...). A typo never becomes a silent runtime bug.
- Nested structs, pointers to structs, slices and maps of structs are validated recursively; recursive types are supported.
- Checks only your handler can make: `return aemeath.ValidationError(aemeath.FieldError{Field: "email", Rule: "unique", Message: "is already taken"})`.
- Malformed input (invalid JSON, `?page=abc`) is a `400`; a body over 1 MiB (`app.SetMaxBodyBytes`) is `413`; every other body, including one a handler reads itself from `c.R.Body`, is capped at 32 MiB by default (`app.SetMaxRequestBytes`) and returning the read error gives `413`; a non-JSON `Content-Type` is `415`. With `curl -d`, add `-H 'Content-Type: application/json'`.

---

## 🗂️ Route Groups & Middleware

```go
admin := app.Group("/admin", authMiddleware)
admin.Options(aemeath.Tags("admin"), aemeath.RequireAuth()) // applies to every typed route below

aemeath.GET(admin, "/dashboard", dashboard)
v1 := app.Group("/api/v1")
users := v1.Group("/users") // inherits /api/v1 and its middlewares
```

- A group's `"/"` route matches the prefix exactly (`/admin`), not everything below it.
- Register a group's middlewares and options **before** creating sub-groups; a sub-group copies them when it is created.

### Middleware

Middlewares run in the order they are registered (the first one is the outermost):

```go
app.Use(
	aemeath.SecurityHeaders(),
	aemeath.RequestID(),
	aemeath.Logger(),          // outside Recover, so it logs the final status (a recovered panic shows as 500)
	aemeath.Recover(),
	aemeath.CORS(),
	aemeath.Gzip(),
	aemeath.MaxBodySize(2<<20),
)
```

| Middleware | What it does |
| :--- | :--- |
| `Logger()` / `SlogLogger(l)` | access log (plain text / structured `slog`: method, path, status, duration_ms, request_id); keeps `http.Flusher`/`Hijacker` working |
| `Recover()` | recovers panics, logs the stack, answers `500` |
| `RequestID()` | reads a safe `X-Request-Id` or makes one; echoed in the response and the logs |
| `CORS(cfg...)` | CORS headers and preflight. Credentials cannot be combined with the `*` origin (panics at startup) |
| `SecurityHeaders()` | `X-Frame-Options`, `X-Content-Type-Options`, `Referrer-Policy`, HSTS (only over TLS) |
| `Gzip(min...)` | compresses JSON/text for clients that accept it; leaves streams, tiny bodies and WebSockets alone |
| `Timeout(d)` | deadline on `c.Context()`; `504` when a handler that honors the context runs out |
| `MaxBodySize(n)` | sets the body limit in place of the default cap (raise it on an upload group, `-1` removes it); reading past it is `413` |
| `RateLimiter(n, window)` | per-IP fixed window; sends `Retry-After` on `429` |
| `RealIP(trusted...)` | behind trusted proxies, takes the client address from `X-Forwarded-For` / `X-Real-IP` |

> **Behind a reverse proxy** (Railway, Fly.io, Render, a Kubernetes ingress), every request comes from the proxy's address. Put `aemeath.RealIP(aemeath.PrivateNetworks...)` before `RateLimiter` and the loggers so they see the real client. Headers from any other address are ignored, so clients cannot spoof them; only use `PrivateNetworks` when the app is not reachable directly from the internet.

### Health checks

`app.EnableHealth()` adds `GET /healthz` (liveness) and `GET /readyz` (readiness: `200`, or `503` when a check added with `app.AddReadyCheck(name, fn)` fails). The database and cache plugins register their own checks.

On Kubernetes or behind another load balancer, add `app.SetShutdownDelay(10 * time.Second)`: after SIGTERM, `/readyz` answers `503` at once while the server keeps serving for that long, so traffic is moved away before connections are refused. The default is `0`.

---

## 📦 Packages

Import only what you use; each plugin's dependencies are only compiled into your binary if you import it.

| Package | Provides | Third-party dependency |
| :--- | :--- | :--- |
| `aemeath` (core) | routing, validation, OpenAPI, TypeScript/Zod/hooks generators, middleware (Logger, Recover, CORS, Gzip, RateLimiter, ...), static files, local file storage | **none** |
| `aemeath/aetest` | in-process typed test client | none |
| `aemeath/jwt` | Bearer auth: secret (HS*), public key (RS*/PS*/ES*/EdDSA) or JWKS URL; `jwt.Issue` | `golang-jwt/jwt` |
| `aemeath/session` | server-side sessions (`MemoryStore`; Redis store in `aemeath/cache`) | none |
| `aemeath/csrf` | CSRF token + Origin checks for cookie auth | none |
| `aemeath/oidc` | OpenID Connect login: discovery, PKCE, nonce, ID-token verification | `golang-jwt/jwt`, `golang.org/x/oauth2` |
| `aemeath/metrics` | Prometheus metrics and an HTTP middleware | none |
| `aemeath/tracing` | W3C Trace Context and an OTLP/HTTP span exporter | none |
| `aemeath/ws` | WebSocket upgrade (same-origin by default) | `gorilla/websocket` |
| `aemeath/db` | GORM plugin; SQLite built in | `gorm`, `glebarez/sqlite` |
| `aemeath/db/postgres`, `aemeath/db/mysql` | PostgreSQL and MySQL/MariaDB drivers for the same plugin | `gorm.io/driver/postgres` / `mysql` |
| `aemeath/cache` | Redis plugin and Redis session store | `go-redis` |
| `aemeath/mailer` | SMTP mailer | `gomail` |
| `aemeath/oauth2` | OAuth2 login (Google, GitHub, ...) | `golang.org/x/oauth2` |
| `aemeath/jobs` | background jobs: queue, workers, retries, periodic jobs | none |

### Plugins and typed dependencies

A plugin provides a service once at startup; handlers read it with a type-safe getter:

```go
plugin, err := db.Open("app.db", &User{}) // migrates your models
if err != nil { log.Fatal(err) }
app.RegisterPlugin(plugin)                 // also adds a "database" readiness check

aemeath.GET(app, "/users", func(c *aemeath.Ctx, _ struct{}) ([]User, error) {
	var users []User
	return users, db.From(c).Find(&users).Error // *gorm.DB; panics with a clear message if not registered
})
```

Your own services work the same way: `aemeath.Provide(app, mySvc)` then `aemeath.Get[*MySvc](c)` / `aemeath.MustGet[*MySvc](c)` (for an interface, `aemeath.Provide[MyIface](app, impl)`).

JWT:

```go
group := app.Group("/api", jwt.Middleware(jwt.Config{SecretKey: os.Getenv("JWT_SECRET")}))
group.Options(aemeath.RequireAuth()) // 401 unless a middleware authenticated the caller; also documented in /docs
// in a handler: jwt.UserID(c), jwt.Claims(c)
```

Tokens must carry `exp` unless `AllowNoExpiry` is set; an empty secret panics at startup.

Sessions, CSRF, OpenID Connect, PostgreSQL/MySQL, forms, metrics and tracing have their own pages in the [documentation](https://naufalraism.github.io/aemeath/): [Authentication](docs/guide/auth.md), [Databases](docs/guide/database.md), [Forms and uploads](docs/guide/forms.md), [Metrics and tracing](docs/guide/observability.md), and a [step-by-step tutorial](docs/guide/tutorial.md) whose finished code is [`examples/notes`](examples/notes).

```go
// A form with a validated image upload
type AvatarIn struct {
	Name   string                `form:"name" validate:"required"`
	Avatar *multipart.FileHeader `form:"avatar" accept:"image/png,image/jpeg" maxsize:"2MB" validate:"required"`
}

// Postgres instead of SQLite
plugin, err := postgres.Open(os.Getenv("DATABASE_URL"), db.WithModels(&User{}))

// Sessions + CSRF for a browser app
app.Use(session.Middleware(session.Config{Store: session.NewMemoryStore()}), csrf.Middleware(csrf.Config{}))
```

Other notes:

- `db` and `cache` close their connection when the server shuts down; register your own cleanup with `app.OnShutdown(func(ctx context.Context) error { ... })` (hooks run in reverse order, each with 10 seconds of its own).
- `LocalStorage.Save` returns a unique stored name (`<unix>_<random>_<original>`) and strips any directory part, replaces control and invisible formatting characters with `_`, and appends `.txt` to names a browser would render or run (`.html`, `.svg`, `.js`, ...). `Static` sends every file with `X-Content-Type-Options: nosniff` and a file of unknown type as `application/octet-stream`, so a served upload folder cannot become stored XSS. It does **not** reject file types: use the `accept` tag, which checks the file's bytes. Files are private to the server's user (mode `0600` on Linux and macOS; on Windows, access follows the folder's permissions); if a reverse proxy must read them, set `FileMode` and `DirMode` (for example `0o644` and `0o755`).
- For OAuth2, make the `state` with `oauth2.GenerateState()`, keep it in a cookie, and compare it in the callback. See `examples/basic`.
- WebSockets only accept **same-origin** browser connections by default. To allow others, set `ws.Upgrader.CheckOrigin`. Messages are limited to 1 MiB (`ws.DefaultReadLimit`), and a connection whose client stops answering pings is closed after 60 seconds (`ws.DefaultIdleTimeout`; the server pings every 30 seconds, and browsers answer by themselves).

---

## ⏱️ Background jobs

`aemeath/jobs` runs work inside the process: a bounded queue, a pool of workers, retries with exponential backoff and periodic jobs. It stops cleanly when the server shuts down.

```go
type SendWelcomeEmail struct{ To string }

func (j *SendWelcomeEmail) Run(ctx context.Context) error { /* ... */ return nil }

runner := jobs.New(jobs.WithWorkers(4), jobs.WithRetries(2))
app.RegisterPlugin(runner)                          // starts the workers; stops them on shutdown

// in a handler:
if err := jobs.From(c).Enqueue(&SendWelcomeEmail{To: user.Email}); err != nil { // never blocks
	return err // jobs.ErrQueueFull when there is no room
}

runner.Every(time.Hour, &CleanupExpiredSessions{})  // skipped while the previous run is unfinished
```

A job that returns an error or panics is retried (3 attempts by default); the final failure is logged with `slog`. **The queue is in memory**: jobs that are waiting when the process crashes are lost, so use a durable queue for work that must never be lost. `aemeath generate job SendEmail` creates the job and registers the runner in `main.go`.

---

## 🧰 TypeScript: client, Zod schemas, React hooks

`app.EnableClient()` serves three files generated from your Go types; `app.WriteTypeScript("web/src/api")` writes them to disk.

| File | Needs | What it is |
| :--- | :--- | :--- |
| `client.ts` | nothing | typed `fetch` client, `ApiError` with per-field `errors` |
| `schemas.ts` | `zod` | one Zod schema per type, plus `<operation>BodySchema`, `...QuerySchema`, `...PathSchema`, `...HeadersSchema` |
| `hooks.ts` | `@tanstack/react-query` v5 | `createHooks(client)`: `GET` → `useQuery`, everything else → `useMutation` |

With the [Aemeath CLI](https://github.com/naufalraism/aemeath-cli) you do not even have to open the URL:

```bash
aemeath ts --out web/src/api           # build the project, write the three files, exit
aemeath dev --ts-out web/src/api       # hot reload that also rewrites them after every successful build
```

The CLI cannot read Go types from outside, so it builds your program and runs it with `AEMEATH_TS_OUT=<dir>`; `app.Listen` sees the variable, writes the files and returns without serving. Nothing to add to `main.go`, but code that runs before `Listen` (opening a database, ...) does run. Files whose content did not change are left untouched, so Vite or webpack do not reload for nothing.

```ts
import { createClient } from "./client";
import { postUsersBodySchema } from "./schemas";
import { createHooks } from "./hooks";

const client = createClient({ baseUrl: "http://localhost:3000" });
const { useGetUsers, usePostUsers } = createHooks(client);

postUsersBodySchema.safeParse(formValues); // the same rules the server enforces
```

The generated Zod schemas and the server make the same accept/reject decision: a test compares them on more than 120,000 values in node, and 4,000 random regular expressions on 140 strings each. A few details:

- **Input**: a field is required only if it has `validate:"required"` (or is a non-pointer struct containing a required field). `oneof` becomes a literal union / `z.enum`. Constraints appear as JSDoc and Zod checks. A required pointer is not nullable.
- **Output**: a field is required unless it has `json:",omitempty"`. Nil slices and maps are nullable (`Array<T> | null`, `Record<K, V> | null`), because Go encodes them as `null`; initialize them with `make([]T, 0)` to send `[]`. Pointers are typed `T | null`.
- `pattern` rules are Go (RE2) expressions. They are translated so that JavaScript (with the `u` flag) accepts exactly the same strings: `.` is `[^\n]`, `\s` is `[\t\n\f\r ]`, `\pL` and `[[:alpha:]]` are spelled the JavaScript way, and so on. The few constructs with no faithful translation (`(?m)`, flags in the middle of a pattern, `\b` or `\p` together with `(?i)`) are left out of Zod and OpenAPI; the server still enforces them.
- Cookie parameters are not part of the browser client (the browser manages cookies).
- The function name comes from `aemeath.OperationID("...")`, or from the method and path (`getHelloByName`).

### Go client and tests

Share one contract between server and client, and call it in tests without a network:

```go
var Hello = aemeath.NewEndpoint[HelloIn, HelloOut]("GET", "/hello/{name}")

aemeath.Register(app, Hello, handler)                                    // server
out, err := Hello.Call(ctx, aemeath.NewClient("http://localhost:3000"), HelloIn{Name: "Ana"}) // real client
out, err = Hello.Call(ctx, aetest.NewClient(app), HelloIn{Name: "Ana"})  // in-process, for tests
```

Responses with status ≥ 400 are returned as `*aemeath.HTTPError`, with `Errors` set for validation failures. Zero values of non-pointer parameters are omitted; use a pointer (`*bool`, `*int`) to send an explicit `false` or `0`.

---

## 📖 OpenAPI

```go
app.EnableDocs(aemeath.Info{Title: "My API", Version: "1.0.0"})
```

`GET /openapi.json` (OpenAPI 3.0.3) and `GET /docs` (the Scalar UI, loaded from a CDN). Named structs reached from an output, and named structs reached *inside* an input (a shared `Address`, for example), become `components.schemas` entries (`$ref`), the latter named `<Type>Input`; the input struct of the operation itself is written into the operation rather than as a component, validation rules become constraints (`minLength`, `enum`, `format`, ...), `description`/`example` struct tags are used, and every operation documents `400`/`422`/`500` (plus `401` with `aemeath.RequireAuth()`). Extra route options: `Description`, `Deprecated`, `Tags`, `Summary`, `OperationID`.

## 📂 Static files & server

```go
app.Static("/public", "./public") // no directory listings, no dotfiles, no links leading outside ./public; global middlewares apply
if err := app.Listen(":3000"); err != nil { // read header 5 s, read 15 s, write 15 s, idle 60 s, headers 64 KiB
	log.Fatal(err)
}
```

`Listen` returns `nil` after a graceful shutdown (Ctrl+C, `SIGTERM`), so do not write `log.Fatal(app.Listen(...))`: `log.Fatal(nil)` prints `<nil>` and exits with status 1, which makes a clean shutdown look like a crash to Docker, Kubernetes and CI. Check the error instead, as above.

For custom timeouts, limits or TLS: `srv := app.NewServer(":3000"); srv.WriteTimeout = 0; app.Serve(srv)`, or `app.ListenTLS(addr, cert, key)`. Requests whose headers exceed 64 KiB (`aemeath.DefaultMaxHeaderBytes`; net/http alone allows 1 MiB) get `431`; raise `srv.MaxHeaderBytes` if your clients send very large cookies.

What the server does under pressure (each point is tested in `internal/hostile`):

- **Shutdown**: after SIGINT/SIGTERM new connections are refused, streams and WebSockets end at once, and requests in flight get 10 seconds (`app.SetShutdownTimeout(d)` changes it). After that their connections are closed: the client gets no response. Keep `SetShutdownDelay` plus this timeout below the platform's grace period.
- **Slow clients** are closed by the timeouts above: headers after 5 s, a body after 15 s, a response the client does not read after 15 s, an idle keep-alive after 60 s.
- **No limit on the number of connections**: a thousand slow clients cost a thousand goroutines until the timeouts close them. On the open internet, put a reverse proxy or load balancer (nginx, Caddy, a cloud load balancer) in front to cap connections per client.
- **`Timeout(d)`** cancels `c.Context()` and answers `504`, but cannot stop a handler that ignores its context: such a handler runs to the end and its response is discarded. Pass `c.Context()` to database and HTTP calls.
- **A client that leaves** cancels `c.Context()`; the request is recorded as `499`, not as a server error. `c.Context()` is also cancelled once the response is sent, so background work must use `context.WithoutCancel(c.Context())` or the `jobs` package.

## 🔄 Upgrading from the first versions

Plugins moved to their own packages and now provide typed dependencies instead of `c.State`:

| Before | Now |
| :--- | :--- |
| `aemeath.JWTMiddleware(aemeath.JWTConfig{...})` | `jwt.Middleware(jwt.Config{...})`; `jwt.UserID(c)` |
| `aemeath.OpenDB(file, models...)`, `c.State["db"].(*gorm.DB)` | `db.Open(file, models...)`, `db.From(c)` |
| `aemeath.NewCache(...)`, `c.State["cache"]` | `cache.New(...)`, `cache.From(c)` |
| `aemeath.NewMailer(...)`, `c.State["mailer"]` | `mailer.New(...)`, `mailer.From(c)` |
| `aemeath.NewOAuth2(...)`, `aemeath.OAuthFrom(c, "google")` | `oauth2.New(...)`, `oauth2.From(c, "google")` |
| `c.Upgrade()` | `ws.Upgrade(c)` |
| `c.State["storage"].(aemeath.Storage)` | `aemeath.MustGet[aemeath.Storage](c)` |

Validation failures are now `422` with a list of field errors (they were `400` with a single message).

## 🧪 Testing

```bash
go test -race ./...
go test -run '^$' -fuzz '^FuzzBind$' -fuzztime 30s .   # fuzz the request binder
```

## 📝 Technical Notes

- Route paths use Go 1.22 syntax: `/hello/{name}`, not `/hello/:name`.
- A request body is only read for `POST`, `PUT` and `PATCH`; `DELETE` bodies are not supported.
- JSON, form (`application/x-www-form-urlencoded`) and multipart bodies are supported. Other content types are not bound; read them from `c.R`.
- OpenAPI is 3.0.3 (no 3.1), without per-operation request size limits or content negotiation.
- Middlewares attached with `Use` take effect when a route first serves a request; do not call `Use` while the server is running.
- Not included yet: the OpenTelemetry SDK (tracing exports OTLP/HTTP itself), a durable job queue (the queue lives in memory), and typed SSE in the TypeScript client.

## 🤝 Contributing & security

> **Security status:** Aemeath has not been independently audited. It is tested (including race, fuzz and static analysis in CI) and built to be secure by default, but you remain responsible for how you configure and deploy your app. Report problems privately, see [SECURITY.md](SECURITY.md).

See [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md). For an honest comparison with Huma, Fuego, Gin and others, see [docs/guide/comparison.md](docs/guide/comparison.md).

## 📝 License

MIT License. See [LICENSE](LICENSE).
