# Changelog

All notable changes to this project are documented here.

## [0.3.1] - 2026-11-14

### Fixed

- **`session.MemoryStore`** no longer grows without bound: expired sessions are swept automatically and live sessions are capped at 100,000 (`WithMaxSessions` changes it). When full, new visitors get no session until older ones expire; existing sessions are unaffected.

## [0.3.0] - 2026-11-12

### Added (authentication, databases, forms, observability)

- **`aemeath/db/postgres` and `aemeath/db/mysql`**: PostgreSQL and MySQL/MariaDB for the `db` plugin through `db.New(dialector, opts...)`, with `db.WithModels`, `db.WithPool` (default 25 open / 5 idle / 30 minutes) and `db.WithConfig`. Errors are translated across databases (`TranslateError`), `db.Translate` maps them to 404/409, and MySQL always scans times into `time.Time`. The same test suite runs against SQLite, PostgreSQL and MySQL. **Your `go.mod` needs `gorm.io/driver/postgres`, `gorm.io/driver/mysql` and `github.com/go-sql-driver/mysql`: run `go mod tidy`.**
- **`aemeath/session`**: server-side sessions with random 43-character ids in an `HttpOnly`, `Secure`, `SameSite=Lax` cookie. Unknown ids are never adopted, `Renew` deletes the old session (fixation defence), `Destroy` for logout, sliding expiry, `MemoryStore`; **`cache.NewSessionStore`** keeps them in Redis.
- **`aemeath/csrf`**: token stored in the session, `X-CSRF-Token` header, Origin check with `TrustedOrigins`, optional readable cookie for front-end libraries.
- **`aemeath/jwt`**: public-key verification (`PublicKey`: RS*/PS*/ES*/EdDSA), `JWKSURL`/`JWKS` with caching, rotation handling and rate-limited refresh (https only; RSA keys under 2048 bits rejected), `Issuer`, `Audience`, `Leeway`, algorithm-confusion defence, `jwt.Issue`, and `jwt.NewVerifier` for use outside middleware.
- **`aemeath/oidc`**: OpenID Connect login (discovery that must match the issuer exactly, PKCE S256, nonce, single-use state, ID-token verification through JWKS, `azp` check), built on the session.
- **Forms and uploads** in the core: the `form:` tag (urlencoded and multipart), `*multipart.FileHeader` and `[]*multipart.FileHeader` fields, `accept` (checked against the file's bytes) and `maxsize` tags, `DefaultMaxMultipartBytes` (32 MiB), temporary files removed after the handler, form values never read from the URL. OpenAPI documents them as `multipart/form-data` or urlencoded bodies; the TypeScript client sends `FormData`; Zod schemas use `z.instanceof(Blob)`.
- **`aemeath/metrics`**: Prometheus text exposition with counters, gauges and histograms, an HTTP middleware labelled by route pattern (never the raw path; unmatched requests share one series), a cap on label combinations.
- **`aemeath/tracing`**: W3C `traceparent` propagation, server and child spans, and an OTLP/HTTP (JSON) exporter with a bounded queue that never blocks a request. Not the OpenTelemetry SDK.
- **CI** now runs the framework tests on Linux, Windows and macOS, runs the database tests against real PostgreSQL and MySQL services, and builds the documentation site (deployed to GitHub Pages from `main`).
- **Documentation**: guides for authentication, databases, forms, metrics and tracing, and a tutorial whose finished code is `examples/notes` (tested in CI).

### Changed

- The `db` plugin's default GORM logger no longer logs "record not found" as an error, and logs only warnings and above. Pass `db.WithConfig` to restore the old behaviour.

## [0.2.0] - 2026-11-03

### Breaking changes

- **Plugins moved to their own packages**, so the core has no third-party dependencies and `go get` no longer pulls gorm, redis, gomail or oauth2 into every project. Services are now provided and read with typed getters instead of `c.State`. See the table in the README ("Upgrading from the first versions"): `aemeath/jwt`, `aemeath/db`, `aemeath/cache`, `aemeath/mailer`, `aemeath/oauth2`, `aemeath/ws`. `NewDBPlugin`/`OpenDB` and the example `User` model were removed from the core.
- **Validation failures are `422`** with `{"error":"validation failed","errors":[{field, rule, message}]}` (they were `400` with one message). All failures are reported together.
- Validation errors name path/query/header/cookie fields by their parameter name, not the Go field name.
- Embedded structs in input types are now supported (they used to panic).
- The OpenAPI document uses `components.schemas` with `$ref` (input types are named `<Type>Input`), so a consumer that expected inline schemas must follow the references.
- Generated TypeScript: required pointers are no longer `| null`, required non-pointer structs are no longer optional, nil slices/maps in responses are `| null`.

### Fixed (late)

- **`log.Fatal(app.Listen(...))` exited with status 1** after every clean shutdown (`log.Fatal(nil)` prints `<nil>` and exits 1), so Docker, Kubernetes and CI saw a crash. The README, docs, example and CLI template now check the error instead, and a test keeps the idiom out of the docs.

### Added (jobs, shutdown, database)

- **`aemeath/jobs`**: background jobs with a bounded queue, a worker pool, retries with exponential backoff, periodic jobs (`Every`, never piling up behind a slow run), panic recovery, `Stats()`, and a clean shutdown. In-memory only: waiting jobs are lost if the process crashes.
- **`App.OnShutdown`**: hooks that run when the server stops, in reverse order, within 10 seconds; a failing hook does not stop the others. They do not run when the program only writes TypeScript (`AEMEATH_TS_OUT`).
- The `db` and `cache` plugins now close their connection at shutdown (`db.Plugin.Close`, `cache.Plugin.Close`).
- Tests for the `db` package against a real SQLite database (they close it, because Windows cannot delete an open database file), and for shutdown hooks.

### Added

- **`AEMEATH_TS_OUT`**: when set, `app.Listen`/`Serve`/`ListenTLS` write the TypeScript files there and return without serving. It is what `aemeath ts` and `aemeath dev --ts-out` use. `WriteTypeScript` now skips unchanged files and writes atomically.
- **Validation rules**: `min`, `max`, `len`, `gt`, `gte`, `lt`, `lte`, `oneof`, `email`, `url`, `uuid`, `pattern`, with nested paths (`items[1].qty`), startup panics for invalid rules, and `aemeath.ValidationError` for handler-side checks.
- **OpenAPI**: reusable components and `$ref` (recursive types work), validation constraints, `description`/`example` tags, `400`/`401`/`422`/`500` responses, Bearer security scheme (`RequireAuth`), header and cookie parameters, `nullable`, `Description`, `Deprecated`, `Group.Options`.
- **TypeScript**: `schemas.ts` (Zod schemas that apply the server's rules) and `hooks.ts` (TanStack Query v5), `ApiError.errors`, header parameters, `oneof` literal unions, JSDoc constraints, `WriteTypeScript(dir)`. `EnableClient` now serves `/client.ts`, `/schemas.ts` and `/hooks.ts`.
- **Binding**: `header:` and `cookie:` sources, `encoding.TextUnmarshaler` types (`time.Time`, UUIDs), embedded structs.
- **Typed dependencies**: `Provide`, `Get`, `MustGet`, `Lookup`.
- **Responses**: `Handled` output type, `Ctx.Redirect`, `File`, `Text`, `SetCookie`, `Ctx.SSE()` (Server-Sent Events), `App.OnError`, `AsHTTPError`.
- **Operations**: `RequestID`, `SlogLogger`, `Gzip`, `Timeout`, `EnableHealth` (`/healthz`, `/readyz`) with `AddReadyCheck` (the database and cache plugins register their own).
- **Testing**: `aetest.NewClient` (typed in-process client), a binding fuzz test, and tests for the JWT, WebSocket, cache (miniredis) and OAuth2 packages.
- `jwt.Claims`, `jwt.UserID`, `oauth2.From`, `oauth2.GenerateState`, `ws.Upgrader`.
- Process: `SECURITY.md`, `CONTRIBUTING.md`, issue and pull request templates, a comparison page, and CI that also checks the core stays dependency-free, fuzzes the binder briefly and type-checks the generated TypeScript.

### Fixed

- **Crash:** registering a recursive input/output type (for example a tree with `Children []*Node`) overflowed the stack at startup.
- **405 Method Not Allowed** was reported as 404. It is now a proper 405 with an `Allow` header.
- 404 and 405 responses (and plain `OPTIONS`) now pass through the global middlewares, so they are logged, rate limited, and carry CORS and security headers.
- **Sibling groups shared middlewares:** `Group.Group` could overwrite a sibling's middleware through slice aliasing.
- `GroupGET(g, "/", ...)` registered a catch-all subtree (`/admin/` matched `/admin/anything`). It is now an exact match on the prefix.
- `Group.Use` called after routes were registered had no effect; it now applies like `App.Use`.
- **Rate limiter** window was anchored to the last request instead of the first, so a slow client could be blocked forever. Its cleanup goroutine (which could never be stopped) is gone, and `429` responses now carry `Retry-After`.
- Go client dropped pointers to zero values (`*int` pointing to `0`, `*bool` pointing to `false`) from the query string.
- `validate:"required"` is now allowed on slices and maps (it used to panic at startup).
- Validation errors for `path`/`query` fields now use the parameter name instead of the Go field name.
- Responses with status 204/304 no longer try to write a body.
- `Logger()` hid `http.Flusher`, which broke streaming responses (SSE).
- Oversized request bodies return **413** instead of 400; non-JSON bodies return **415** instead of a confusing "invalid JSON".
- `LocalStorage` could overwrite a file uploaded with the same name within the same second; stored names now contain a random component.
- TypeScript/OpenAPI generation now flattens embedded structs like `encoding/json` does, and marks nil-able slices and maps as nullable.
- The generated TypeScript runtime no longer throws a `SyntaxError` on non-JSON error bodies.
- GCS and S3 provider templates: GCS upload errors on `Close` were ignored; names could collide.

### Security

- `LocalStorage` now creates uploaded files with mode `0600` in a `0750` directory (it was `0644`/`0755`, readable by every user on the server). If another program must read them (a reverse proxy serving `/uploads`, for example), set `FileMode` and `DirMode`.
- `Static` no longer lists directory contents.
- `JWTMiddleware` panics on an empty `SecretKey`, only accepts HS256/384/512, and requires an `exp` claim unless `JWTConfig.AllowNoExpiry` is set.
- WebSocket upgrades are same-origin by default (was: any origin). Configure `WebSocketUpgrader.CheckOrigin` to allow others.
- `CORS` panics if `AllowCredentials` is combined with the `*` origin; a partial `CORSConfig` keeps the default methods and headers.
- `SecurityHeaders` sends `X-XSS-Protection: 0`, `Referrer-Policy`, and HSTS (TLS only).
- The access logs escape the request path, so crafted URLs cannot forge log lines.
- OAuth2 example now uses a random `state` checked in the callback.

### Added (smaller items)

- Query parameters can be slices (`?tag=a&tag=b`) in the server, Go client, and TypeScript client.
- `App.NewServer`, `App.Serve`, `App.ListenTLS` for custom timeouts and TLS.
- `Group.Handle`, `Ctx.Context`, `OpenDB`, `OAuthFrom`, `GenerateState`, `CachePlugin.Close`, `WebSocketUpgrader`, `StateJWTClaims`, `StateUserID`.
- Error helpers: `PayloadTooLarge`, `UnsupportedMediaType`, `TooManyRequests`, `Conflict`, `Forbidden`, `MethodNotAllowed`.
- A startup warning when a `GET`/`DELETE` input has a `json`-tagged field that can never be bound.

### Changed

- All code comments, messages, docs and examples are now in English (error messages such as `"... wajib diisi"` are now `"... is required"`).
- `Static` now goes through global middlewares.
- `OAuthPlugin` stores itself under `oauth_<provider>` (the old `oauth` key remains, last registered wins).
- `NewDBPlugin` is deprecated in favor of `OpenDB`.
- `RateLimiter` panics on non-positive arguments.
- Repository: line endings normalized to LF, `statis_test.go` renamed `static_test.go`, benchmark no longer reuses one `ResponseRecorder`, README benchmark claims softened, unsourced benchmark figures and testimonials removed from the landing page.