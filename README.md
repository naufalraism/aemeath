# Aemeath 🚀

[![Go](https://github.com/naufalraism/aemeath/actions/workflows/go.yml/badge.svg)](https://github.com/naufalraism/aemeath/actions/workflows/go.yml)
[![golangci-lint](https://github.com/naufalraism/aemeath/actions/workflows/golangci-lint.yml/badge.svg)](https://github.com/naufalraism/aemeath/actions/workflows/golangci-lint.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/naufalraism/aemeath.svg)](https://pkg.go.dev/github.com/naufalraism/aemeath)
[![GitHub release](https://img.shields.io/github/v/release/naufalraism/aemeath?sort=semver&display_name=tag&cacheSeconds=3600)](https://github.com/naufalraism/aemeath/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**Write your API once in Go. Get the docs, the TypeScript client, the Zod schemas and the React hooks for free.**

The fastest way to start a new Aemeath project is by using the official CLI [Aemeath CLI](https://github.com/naufalraism/aemeath-cli).

Aemeath is a Go web framework built on the standard `net/http` (Go 1.22+ `ServeMux` patterns). You describe each route with a plain input struct and an output struct. From those same structs Aemeath derives request binding, validation, OpenAPI, a typed TypeScript client, Zod schemas and TanStack Query hooks, and they all follow the **same rules the server enforces**.

> **Status:** pre-1.0. The API may still change between minor versions. See the [changelog](CHANGELOG.md).
> The core package has **no third-party dependencies**; plugins live in their own packages (see [Packages](#-packages)).

## ✨ Features

- **Type-safe routes**: define request/response structs once; binding and validation are automatic.
- **Rich validation**: `required`, `min`, `max`, `len`, `gt/gte/lt/lte`, `oneof`, `email`, `url`, `uuid`, `pattern`. Every failure is reported at once as a `422` with per-field errors.
- **One source of truth**: the same tags drive the server, the OpenAPI document (`$ref` components, constraints, security, error schemas), the TypeScript client, the Zod schemas and the React Query hooks.
- **Binding** from path, query (including repeated keys), headers, cookies and JSON; embedded structs; `time.Time`, UUIDs and any `encoding.TextUnmarshaler`.
- **Typed dependencies**: `aemeath.Get[*gorm.DB](c)` instead of `c.State["db"].(*gorm.DB)`.
- **Operations**: request IDs, structured logging (`slog`), gzip, timeouts, `/healthz` and `/readyz`, graceful shutdown.
- **Realtime**: Server-Sent Events and WebSockets.
- **Testing**: `aetest` calls your app in-process through the same typed endpoints.
- **Security by default**: strict JWT, same-origin WebSockets, no directory listings, no CORS wildcard with credentials.

---

## 📊 Performance

Aemeath adds typed binding and validation on top of `net/http` for a small fixed cost per request.

*Microbenchmark of a single JSON route using `httptest`, on an Intel i7-10750H with Go 1.27 (lower is better).*

| Framework | Time (ns/op) | Memory (B/op) | Allocations |
| :--- | :--- | :--- | :--- |
| `net/http` (baseline) | 1,246 | 1,040 B | 10 |
| **Aemeath** | **2,238** | **1,120 B** | **13** |
| Gin | 2,521 | 1,434 B | 16 |

This is a microbenchmark, not a load test: numbers vary by machine and say nothing about real-world throughput, where your handlers and database dominate. The table above was measured before the validation engine was added; re-measured afterwards on another machine, a plain `GET` still costs 13 allocations (about 1.1 KB), and a JSON `POST` with two validated fields costs about 31 allocations (7.3 KB), including the cost of building the test request. Measure your own routes:

```bash
go test -run '^$' -bench . -benchmem .      # Aemeath alone
cd benchmarks && go test -bench . -benchmem # comparison with Gin and Fiber
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
aemeath new my-project
aemeath generate api Product    # CRUD handlers
aemeath generate test Product   # tests for them
aemeath dev                     # hot reload
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
sse, err := c.SSE()          // disables the write timeout for this stream
sse.Send("tick", map[string]int{"n": 1})
```

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
| `email`, `url`, `uuid` | string | formats (`url` must be absolute http/https) |
| `pattern=REGEX` | string | regular expression (RE2); no commas |

How it behaves:

- **All** failures are collected and returned together as `422`:
  `{"error":"validation failed","errors":[{"field":"age","rule":"gte","message":"must be at least 18"}]}`. Nested fields are named `address.city`, `items[1].qty`, `extra[home].city`.
- Rules other than `required` only run when a value was provided, so an optional field can still be constrained.
- For **scalars** (`int`, `bool`, `float`) with `required` you **must** use a pointer so "not sent" differs from "sent as zero". Aemeath panics at **startup** for that, for `required`+`omitempty`, and for unknown or misapplied rules (`min` on a bool, `email` on a number, a bad regex, ...). A typo never becomes a silent runtime bug.
- Nested structs, pointers to structs, slices and maps of structs are validated recursively; recursive types are supported.
- Checks only your handler can make: `return aemeath.ValidationError(aemeath.FieldError{Field: "email", Rule: "unique", Message: "is already taken"})`.
- Malformed input (invalid JSON, `?page=abc`) is a `400`; a body over `aemeath.DefaultMaxBodyBytes` (1 MiB) is `413`; a non-JSON `Content-Type` is `415`. With `curl -d`, add `-H 'Content-Type: application/json'`.

---

## 🗂️ Route Groups & Middleware

```go
admin := app.Group("/admin", authMiddleware)
admin.Options(aemeath.Tags("admin"), aemeath.RequireAuth()) // applies to every typed route below

aemeath.GroupGET(admin, "/dashboard", dashboard)
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
| `MaxBodySize(n)` | caps request bodies; typed routes answer `413` |
| `RateLimiter(n, window)` | per-IP fixed window; sends `Retry-After` on `429` |

> **Behind a reverse proxy**, `RateLimiter` sees the proxy's address for every request (it uses `RemoteAddr`). Limit at the proxy or restore the real client IP first.

### Health checks

`app.EnableHealth()` adds `GET /healthz` (liveness) and `GET /readyz` (readiness: `200`, or `503` when a check added with `app.AddReadyCheck(name, fn)` fails). The database and cache plugins register their own checks.

---

## 📦 Packages

Import only what you use; each plugin's dependencies are only compiled into your binary if you import it.

| Package | Provides | Third-party dependency |
| :--- | :--- | :--- |
| `aemeath` (core) | routing, validation, OpenAPI, TypeScript/Zod/hooks generators, middleware (Logger, Recover, CORS, Gzip, RateLimiter, ...), static files, local file storage | **none** |
| `aemeath/aetest` | in-process typed test client | none |
| `aemeath/jwt` | HS256/384/512 Bearer auth | `golang-jwt/jwt` |
| `aemeath/ws` | WebSocket upgrade (same-origin by default) | `gorilla/websocket` |
| `aemeath/db` | GORM + SQLite plugin | `gorm`, `glebarez/sqlite` |
| `aemeath/cache` | Redis plugin | `go-redis` |
| `aemeath/mailer` | SMTP mailer | `gomail` |
| `aemeath/oauth2` | OAuth2 login (Google, GitHub, ...) | `golang.org/x/oauth2` |

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
group.Options(aemeath.RequireAuth()) // documents the Bearer scheme and a 401 in /docs
// in a handler: jwt.UserID(c), jwt.Claims(c)
```

Tokens must carry `exp` unless `AllowNoExpiry` is set; an empty secret panics at startup.

Other notes:

- `LocalStorage.Save` returns a unique stored name (`<unix>_<random>_<original>`) and strips any directory part; it does **not** check file types. Call `IsAllowedFile` yourself. Files are private to the server's user (mode `0600`); if a reverse proxy must read them, set `FileMode` and `DirMode` (for example `0o644` and `0o755`).
- For OAuth2, make the `state` with `oauth2.GenerateState()`, keep it in a cookie, and compare it in the callback. See `examples/basic`.
- WebSockets only accept **same-origin** browser connections by default. To allow others, set `ws.Upgrader.CheckOrigin`.

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

The generated Zod schemas and the server made the same accept/reject decision for every payload in our parity check. A few details:

- **Input**: a field is required only if it has `validate:"required"` (or is a non-pointer struct containing a required field). `oneof` becomes a literal union / `z.enum`. Constraints appear as JSDoc and Zod checks. A required pointer is not nullable.
- **Output**: a field is required unless it has `json:",omitempty"`. Nil slices and maps are typed `T[] | null` (Go encodes them as `null`); initialize them with `make([]T, 0)` to send `[]`. Pointers are typed `T | null`.
- `pattern` rules are Go (RE2) expressions handed to JavaScript's `RegExp`; stick to the common syntax.
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

`GET /openapi.json` (OpenAPI 3.0.3) and `GET /docs` (the Scalar UI, loaded from a CDN). Named structs become `components.schemas` entries (`$ref`; input types are named `<Type>Input`), validation rules become constraints (`minLength`, `enum`, `format`, ...), `description`/`example` struct tags are used, and every operation documents `400`/`422`/`500` (plus `401` with `aemeath.RequireAuth()`). Extra route options: `Description`, `Deprecated`, `Tags`, `Summary`, `OperationID`.

## 📂 Static files & server

```go
app.Static("/public", "./public") // no directory listings; global middlewares apply
if err := app.Listen(":3000"); err != nil { // read header 5 s, read 15 s, write 15 s, idle 60 s
	log.Fatal(err)
}
```

`Listen` returns `nil` after a graceful shutdown (Ctrl+C, `SIGTERM`), so do not write `log.Fatal(app.Listen(...))`: `log.Fatal(nil)` prints `<nil>` and exits with status 1, which makes a clean shutdown look like a crash to Docker, Kubernetes and CI. Check the error instead, as above.

For custom timeouts or TLS: `srv := app.NewServer(":3000"); srv.WriteTimeout = 0; app.Serve(srv)`, or `app.ListenTLS(addr, cert, key)`.

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
go test -run '^$' -fuzz FuzzBind -fuzztime 30s .   # fuzz the request binder
```

## 📝 Technical Notes

- Route paths use Go 1.22 syntax: `/hello/{name}`, not `/hello/:name`.
- A request body is only read for `POST`, `PUT` and `PATCH`; `DELETE` bodies are not supported.
- Only JSON bodies are supported (no form or multipart binding yet; read those from `c.R`).
- OpenAPI is 3.0.3 (no 3.1), without per-operation request size limits or content negotiation.
- Middlewares attached with `Use` take effect when a route first serves a request; do not call `Use` while the server is running.
- Not included yet: OpenTelemetry/Prometheus integrations, a job runner, typed SSE in the TypeScript client.

## 🤝 Contributing & security

See [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md). For an honest comparison with Huma, Fuego, Gin and others, see [docs/guide/comparison.md](docs/guide/comparison.md).

## 📝 License

MIT License. See [LICENSE](LICENSE).
