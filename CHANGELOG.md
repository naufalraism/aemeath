# Changelog

All notable changes to this project are documented here.

## [Unreleased]

## [0.7.0] - 2026-11-25

### Security

- **Session fixation is much harder.** An attacker who planted a real session id in a victim's browser (from a sibling subdomain or a plain-http page) shared the victim's session after a login that did not call `Renew`; nothing warned about the missing call. Over HTTPS the session cookie is now named `__Host-aemeath_session`, which a browser accepts only from the site itself over HTTPS, so the id cannot be planted. New `s.Login(ctx, key, value)` renews the id and sets the value in one call.
- **`oidc.Complete` and `oauth2.Complete` renew the session id on success.** A successful `Complete` is a login, so the app no longer has to remember `Renew`: the id from before the login (which may have been planted) stops working. Calling `Renew` yourself as well is harmless.

### Breaking changes

- **The session cookie is named `__Host-aemeath_session` over HTTPS** (Secure, Path `/`, no `Domain`), instead of `aemeath_session`. **Users who are logged in when you upgrade log in once more.** With `Insecure`, a `Domain` or another `Path` the name stays `aemeath_session`; a `CookieName` you set is kept. A `__Host-` or `__Secure-` `CookieName` that browsers would refuse now panics at startup.

### Changed

- **Docs and examples log in with `s.Login(...)`**, and no longer call `Renew` after `oidc`/`oauth2` `Complete`, which renews the id itself. The auth guide explains the `__Host-` cookie.

## [0.6.1] - 2026-11-24

### Fixed

- **A `bool` parameter accepts `on` and `off`** (any case), in the query, path, headers, cookies and forms. An HTML checkbox without a `value` attribute submits `on` when ticked, which was refused with 400, so a login form with "remember me" failed as soon as the box was ticked. Other values are still refused.
- **`session.MemoryStore` keeps values as JSON, like the Redis store.** On the next request `s.Get("count").(int)` worked with `MemoryStore` and failed on Redis, which returns `float64`: code that passed on a laptop broke in production. Every store now returns numbers as `float64`, structs as `map[string]any` and slices as `[]any`, and refuses a value JSON cannot encode when saving. **If you type-assert session numbers with `MemoryStore`, use `s.Int(key)`.**
- **`Session.Int` reads every integer type.** It understood only `int` and `float64`, so `s.Int("uid")` of a `uint` (a GORM primary key) gave `0` with `MemoryStore`. It now reads `int8`…`int64`, `uint`…`uint64` and a whole `float64`, and gives `0` for a fraction (it used to round `2.5` down to `2`) or a number that does not fit in an `int`.

## [0.6.0] - 2026-11-24

### Security

- **Upgrade if you use `oauth2` login or MySQL.**
  - `oauth2` now has `Begin` and `Complete`, which keep the login `state` in the server-side session and check it. Before, every app had to create, store and compare the state itself, and an app that skipped a step accepted a forged callback (login CSRF). Move to `Begin`/`Complete` (see Added); `GenerateState` is deprecated.
  - `github.com/go-sql-driver/mysql` is upgraded from 1.10.1 to 1.10.2, which fixes a panic on a truncated handshake packet from the server (GHSA-9qjq-77h9-895w) and the reuse of a connection after a failed `LOAD DATA LOCAL INFILE` (GHSA-4v5x-9mc8-gr98). Aemeath does not use the other changed features (`interpolateParams`, `BeforeConnect`).
- **Two apps in one process no longer share their limits.** The body and validation limits were package variables; one app configuring itself while another served was a data race (see Added).

### Upgrading from 0.5

- The four **Breaking changes** below each need a one-line change where you use them: `jwt.Claims` (a map), `mailer.New` and `cache.New` (a `Config`), and `Client` options instead of fields.
- Everything listed under **Deprecated** still works, so the rest of your code keeps building; your editor (gopls) and staticcheck mark the calls to move. They will be removed before v1.0.
- The CLI that generates `aemeath.GET(group, …)` needs this version: update the framework (`go get github.com/naufalraism/aemeath@v0.6.0`) before the CLI.

### Breaking changes

- **`jwt.Claims`, `Verifier.Verify` and `Verifier.VerifyContext` return `map[string]any`** instead of golang-jwt's `MapClaims`, so the public API no longer depends on that library. Reading a claim is unchanged (`claims["role"].(string)`); only code calling `MapClaims` methods such as `GetSubject` needs to read the map instead (or use `jwt.UserID`).
- **`mailer.New` takes a `mailer.Config`**: `mailer.New(mailer.Config{Host: "smtp.gmail.com", Port: 587, Username: …, Password: …, From: …})`. The plugin's fields (`Dialer`, a gomail type, `From`, `Timeout`) are no longer exported; `Config` holds them, plus `SSL` and `TLSConfig`, which were only reachable through the gomail dialer. Port 465 still means TLS from the start.
- **`cache.New` takes a `cache.Config`**, and can now connect over TLS, which managed Redis services such as Upstash or Redis Cloud require (the old `cache.New(addr, password, db)` could not). `cache.New(cache.Config{URL: os.Getenv("REDIS_URL")})` accepts the `redis://` or `rediss://` URL such services hand out; `Addr`, `Password`, `DB` and `TLS` set them one by one. A malformed URL panics at startup. Migrate with `cache.New(cache.Config{Addr: addr, Password: password, DB: db})`.
- **`Client.HTTP` and `Client.Header` are no longer exported.** Code could change the header map while requests read it, a data race (the race detector reported it). Set them once with `NewClient(url, aemeath.WithHTTPClient(h), aemeath.WithHeader(k, v))`; a `Client` is then safe for concurrent use. `BaseURL` stays exported.

### Added

- **Round-trip tests for the Go client**: every parameter source and the body through one shared `Endpoint`, values that need escaping, nil versus zero pointers, a time with nanoseconds and a zone; parameter fields kept out of the body; and the error paths (422 with field errors, a non-JSON error page, 204, a 200 that is not JSON, a response over 32 MiB, an empty path parameter, a cancelled context, an unreachable server).
- **More shutdown tests on a real process**: an open Server-Sent Events stream is ended by the server and the process exits at once instead of waiting out the grace time, and a job that is running when the signal arrives finishes before the process exits.
- **`Route`, `GET`, `POST`, `PUT`, `PATCH`, `DELETE` and `Register` accept a `Group` as well as an `App`**, through the new `Router` interface: `aemeath.GET(api, "/users/{id}", getUser)`. Inside a group the path is relative to its prefix, and its middlewares and options apply, exactly as with the `GroupGET` family. Code that passes an `*App` is unchanged.
- **Limits per app: `App.SetMaxBodyBytes`, `SetMaxMultipartBytes`, `SetMaxRequestBytes` and `SetMaxValidationErrors`.** They were package variables, so two apps in one process (two parallel tests, say) could not have different limits, and one configuring itself while the other served was a data race (the race detector reported it). Call them before the server starts; they panic afterwards. `Ctx.BodyLimits` gives middleware that reads a form itself the same limits; `csrf` uses it.
- **Package `storage`**: `storage.Storage` and `storage.Local` (`storage.NewLocal`), the file storage that was in the core package, unchanged in behaviour. Register it with `aemeath.Provide[storage.Storage](app, storage.NewLocal("uploads"))`. The package uses only the standard library. Its errors start with `storage:` instead of `aemeath:`.
- **`oauth2.Plugin.Begin` and `Complete`**: a login that keeps the state in the server-side session and checks it (constant time, once, within `MaxLoginAge`), like `oidc`. `Complete` returns the token and the user info. Optional `PKCE`; `HTTPClient` (10-second timeout by default) for the exchange and user info. Before, each app created, stored and compared the state itself, and forgetting a step left the login open to CSRF.

### Deprecated

- **`GroupRoute`, `GroupGET`, `GroupPOST`, `GroupPUT`, `GroupPATCH`, `GroupDELETE` and `RegisterGroup`**: use `Route`, `GET`, `POST`, `PUT`, `PATCH`, `DELETE` and `Register` with the group. They still work and will be removed before v1.0.
- **`DefaultMaxBodyBytes`, `DefaultMaxMultipartBytes`, `DefaultMaxRequestBytes` and `MaxValidationErrors`**: use the `App.SetMax…` methods. An app that does not call them still reads these variables, even when they change after `New`, so existing code behaves as before. They will be removed before v1.0.
- **`Storage`, `StoragePlugin`, `LocalStorage` and `NewLocalStorage`**: use the `storage` package. `aemeath.Storage` is an alias of `storage.Storage`, so a storage provided under one name is found under the other, and `LocalStorage` now calls `storage.Local`. They will be removed before v1.0.
- **`FileLoggerPlugin` and `NewFileLogger`**: `SlogLogger` does the same and more (request ID, panics, levels, JSON). Write to a file with `app.Use(aemeath.SlogLogger(slog.New(slog.NewJSONHandler(f, nil))))` and close it with `app.OnShutdown`. They will be removed before v1.0.
- **`oauth2.GenerateState`**: `Begin` and `Complete` handle the state. It will be removed before v1.0.

### Fixed

- **The Go client no longer alters cookie values silently.** A cookie parameter holding `;`, `"`, `\`, a control character or non-ASCII text was sent with those bytes dropped (net/http only logs it), so the server got `ab` for `a;b` and an empty value for `é` while the call succeeded. `Call` now returns an error naming the cookie and sends nothing.

### Changed

- **Docs, README and examples register group routes with `GET(group, …)`** instead of the deprecated `GroupGET`.
- **Docs and README set limits with `app.SetMax…`** instead of the deprecated package variables.
- **Docs, README, SECURITY.md and examples use the `storage` package and `SlogLogger` to a file** instead of the deprecated `LocalStorage`, `StoragePlugin` and `FileLogger`.
- **`examples/basic` logs in to Google with `oidc` and to GitHub with `oauth2` `Begin`/`Complete`**, instead of Google through plain OAuth2 with a hand-made state cookie; `.env.example` has `GOOGLE_REDIRECT_URL` (was `OAUTH_REDIRECT_URL`) and the GitHub variables. The auth guide, README and plugin list describe the new flow.

## [0.5.0] - 2026-11-23

### Security

- **Upgrade if you use sessions or JWT.** Four fixes, each with a test that failed before:
  - a slow request that finished after a logout logged the user back in (below);
  - application code writing `c.State["user_id"]` changed what `jwt.UserID` reported (see Breaking changes);
  - with `session.Middleware` but without `csrf.Middleware`, a `POST` from another site was served (see Breaking changes);
  - `?price=NaN` passed `validate:"gte=0,lte=100"` (see Fixed).
- **Overlapping requests of one session no longer erase each other, and a logout stays a logout.** Each request saved the whole session it had loaded, so a slow request erased what a faster one saved under another key, and a slow request that finished after a logout stored the session again: the user was logged back in, with a fresh cookie. A request now saves only the keys it changed, on top of what the store holds when it finishes, and does not store a session that was ended meanwhile (`Destroy`, `Renew`, expiry). Two requests changing the same key: the last one still wins. Works with any `session.Store`; costs one extra `Load` on requests that change the session.

### Breaking changes

- **Removed `App.WriteTypeScriptClient`**, an older duplicate of `WriteTypeScript` that wrote only client.ts and not atomically (an interrupted write could leave half a file). Use `app.WriteTypeScript(dir)`, which writes client.ts, schemas.ts and hooks.ts.
- **Removed `cache.Plugin.Set` and `Get`**, one-line wrappers over the Redis client. Handlers already get the client from `cache.From(c)`; use `client.Set(ctx, key, value, ttl).Err()` and `client.Get(ctx, key).Result()`.
- **`metrics.RouteOf` and `tracing.ParseTraceparent` are no longer exported.** They were helpers of the metrics and tracing middleware; keeping them public would have made their behaviour a promise.
- **`Plugin` no longer has `Name()`**, and the built-in plugins no longer have the method. It was never read: `RegisterPlugin` only calls `Apply`. Your own plugins keep compiling (an extra `Name` method is allowed) and can drop it; only code that called `Name()` on a built-in plugin breaks.
- **Float parameters are plain decimal numbers.** A float from the path, query, a header, a cookie or a form no longer accepts digit separators (`1_000`) or hexadecimal floats (`0x1p4`): they get 400, as they already did for int fields and in JSON bodies. `+5`, `.5`, `5.`, `1e3` and leading zeros are still accepted.
- **The verified identity no longer lives in `c.State`.** `jwt.Middleware` stored the token's subject under `c.State["user_id"]` and its claims under `"jwt_claims"`, and `RequestID` the id under `"request_id"`. Application code could overwrite them: a middleware keeping `c.State["user_id"]` for an audit log, filled from the query, made `jwt.UserID` report `"admin"` for alice's token. They are now kept where only their package can write them; read them with `jwt.UserID(c)`, `jwt.Claims(c)` and `c.RequestID()` as before. Removed: `jwt.StateClaims`, `jwt.StateUserID`, `aemeath.StateRequestID`.
- **`tracing.Span` no longer exports the fields that change** (`Attrs`, `Error`, `EndTime`, `Sampled`). Writing `span.Attrs[k]` directly skipped the lock and raced with the exporter (caught by the race detector). Use `SetAttr`, `SetError` and `End` as before; read the sampling decision with `span.Sampled()`. `TraceID`, `SpanID`, `ParentID`, `Name` and `Start` stay exported, to be read only.
- **`session.Middleware` refuses cross-origin writes (403).** Cookies are sent with requests a hostile page triggers; with only the session middleware installed, a `POST` from another site was served. It now applies `net/http.CrossOriginProtection` (by `Sec-Fetch-Site`, or `Origin` on browsers without it) to `POST`, `PUT`, `PATCH` and `DELETE`. Same-origin pages, reads, curl, mobile apps and webhooks are unaffected. A front end on another origin (including another subdomain) must be listed in the new `session.Config.TrustedOrigins`, also when it is already in `csrf.Config.TrustedOrigins`; `AllowCrossOrigin: true` turns the check off.

### Added

- **A list of the public API** in `testdata/api.txt`, checked by `go test`: every exported function and method with its signature, type, field, constant and variable. A change to the public API now shows up in the diff of that file instead of slipping in by accident (`go test -run TestPublicAPI -update-api .` after an intended change).
- **Server limit tests.** `NewServer`'s documented timeouts and header limit are checked, and so is their effect on a real port: a header past 64 KiB is answered 431, and a client that sends its headers a byte at a time (slowloris) is cut off by `ReadHeaderTimeout`. `ListenTLS` is tested end to end with a self-signed certificate (HTTPS answered, plain HTTP refused, a clean stop on a signal, an error for a missing certificate).
- **Shutdown tests on a real process.** The test binary starts itself again as a server and is stopped the way Docker, Kubernetes or a terminal does it (SIGTERM on Unix, Ctrl+Break on Windows): an in-flight request finishes, the hooks run, the port closes and the process exits with status 0; with a short grace time the process gives up on a stuck request and still exits.
- **One contract for every `session.Store`.** `internal/storetest` checks loading a missing id, the round trip, that a save replaces the whole session, empty sessions, delete, expiry, that saving again restarts the TTL, that callers cannot change what is stored through the maps they pass or get back, and concurrent use. `MemoryStore` and the Redis `cache.SessionStore` both pass it; a store that keeps the caller's map and ignores the TTL fails two of its cases.
- **Fuzz targets for `RealIP` and `Static`.** `FuzzRealIP` checks that an untrusted peer can never change its address, that the address handlers see is always a valid `host:port` taken from the headers, and that the right-most untrusted hop wins; `FuzzStaticPaths` checks that no path (`../`, `%2e%2e`, `..\`, dotfiles, ...) ever serves a file outside the folder or a dotfile, and that none answers 500. About 1.5 million inputs each found nothing beyond the zone bug fixed above.
- **Golden files for the generators.** `examples/tscheck/testdata/golden` holds the `client.ts`, `schemas.ts`, `hooks.ts` and `openapi.json` generated for the example app, and `go test ./...` fails when the output changes, showing the first line that differs; `go test ./examples/tscheck -update` rewrites them after an intended change. A second test checks that two runs give the same bytes.
- **`jwt.Verifier.VerifyContext(ctx, token)`**, used by `oidc` for the ID token: with a JWKS the key lookup stops waiting when `ctx` ends. `Verify` is `VerifyContext` with `context.Background()`.
- **`benchmarks/load`: a load test over real TCP.** It starts `net/http`, Aemeath and Gin as separate processes with the same two routes (`GET /hello`, and `POST /users` with validation), checks that each one refuses bad input, then measures requests per second, p50/p95/p99 latency, errors and process memory. `go run ./load all` from `benchmarks/`.

### Fixed

- **`NaN` and infinities no longer slip through binding and validation.** NaN compares false with everything, so `?price=NaN` passed `validate:"gte=0,lte=100"` and reached the handler (a response echoing it then failed with 500); infinities passed any rule without the matching bound. A float from the path, query, a header or a cookie that is not a finite number is now refused with 400, and `min`, `max`, `gt`, `gte`, `lt` and `lte` refuse NaN with 422 wherever it comes from (a float read with `json:",string"` can be `"NaN"`).
- **`RealIP` ignores addresses with an IPv6 zone** in `X-Forwarded-For` and `X-Real-IP`. A zone (`fe80::1%eth0`) only names an interface of the machine that wrote it, and any text was accepted as one: `::%[` became the `RemoteAddr` `[::%[]:4000`, which `net.SplitHostPort` rejects. Found by the new `FuzzRealIP`.
- **JWT with a JWKS: the key lookup follows the request.** It used `context.Background()`, so a request whose client had left still waited for a slow JWKS endpoint until the HTTP client's timeout (3 s in the test, 10 s by default); it now stops with the request. The fetch itself is shared and now runs detached from the request that started it, bounded by the client's timeout: before, when that request went away, every other request waiting for the same key failed with `context deadline exceeded`.

### Changed

- **`LocalStorage.IsAllowedFile` is deprecated** and will be removed in v1.0. It trusts the extension the client chose: an HTML page named `photo.png` passed it. Use the `accept` tag on a `*multipart.FileHeader` field, which checks the file's bytes (that file gets 422). `examples/basic` now uploads this way.
- **README links the website, the documentation and the CLI**, and the website's CLI section lists `aemeath ts` and `aemeath help`; its terminal example shows the `💡 All commands: aemeath help` line that `aemeath new` prints since CLI v0.4.2.
- **README and website: load-test and stress-test results.** Under load over TCP Aemeath serves 96 to 99% of plain `net/http`'s requests per second, on a par with Gin, with 0 errors in about 21 million requests; up to 1,600 connections none of the three servers returned an error. The microbenchmark stays in the README, now labelled as such.

## [0.4.2] - 2026-11-22

### Security

- **`Static` no longer follows a symbolic link out of its folder.** A link such as `public/link.txt -> ../secret.txt` (or a link to a directory such as `/etc`) served the file it pointed to with `200`. Files are now opened with `os.OpenInRoot`, so a link that resolves outside the folder is a `404`; links that stay inside, and a folder that is itself a link, still work.
- **The OIDC token exchange has a time limit.** Without `Config.HTTPClient` it used `http.DefaultClient`, which has none (discovery already used a 10-second client), and x/oauth2 may try a second request, so a slow provider held the login callback for 2 × its delay (30 s against a 15 s token endpoint). The exchange now uses the same client as discovery and one time limit for the whole exchange: the client's timeout, or 10 seconds.
- **`tracing.Config.IgnoreIncomingSampling`** (new, off by default): a service that faces the internet can make the sampling decision itself, so a client cannot send `traceparent: ...-01` to have every request traced and exported despite a low `SampleRatio`. The trace ID still continues. Off by default, because behind your own services a trace sampled upstream should stay complete.
- **A crawler following the OIDC login link no longer fills the session store for a day.** Every `GET` of the login page (`oidc.Provider.Begin`) created a session that lasted the session TTL (24 hours); 150 such requests filled a store capped at 100, so real users got no session and could not log in until they expired. For a visitor without a session, `Begin` now saves it for `MaxLoginAge` (10 minutes); after the login it is saved with the usual TTL. New: `session.Session.IsNew` and `ExpireIn`.
- **`LocalStorage.Save` replaces the characters Windows refuses in a file name** (`<>:"|?*`) with `_`, on every OS. On NTFS a colon names a stream: an upload called `x.html::$DATA` was stored as the page `x.html`, past the `.html` → `.html.txt` rename, and `a.png:evil.html` wrote a hidden stream. Names with `?` or `*` also failed to save on Windows.
- **The Zod schema of an optional `json:",string"` field accepts the string `"null"`**, which encoding/json reads as "not sent", like JSON `null`; a required one still refuses it. (Floats with `,string` remain stricter in Zod than on the server, which also takes forms such as `"NaN"`, `"0x1p4"` and `"1_000"`.)

## [0.4.1] - 2026-11-18

### Fixed

- **A struct tag Go cannot read is refused at startup.** `validate:"required,pattern=^\d{5}$"`, the form the validation guide showed, is not a valid Go string (`\d` is not an escape), so `reflect` returned no `validate` tag and the field lost every rule, `required` included, on the server, in the Zod schemas and in OpenAPI, without a word (only `go vet` warned). Registration now panics and names the field; the guide shows `\\d`.
- **`null` for an optional field of a JSON body** is accepted by the generated Zod schemas, TypeScript types (`note?: string | null`) and OpenAPI, as it is by the server, which reads `null` as "not sent" for any type. Zod used to refuse `{"note": null}` for a non-pointer field that the server accepted. A required field, or a non-pointer struct with a required field, still refuses `null` everywhere.
- **`json:",string"` integers in the Zod schemas** are checked against the range of their Go type and accept a leading `+` where encoding/json does (Go 1.27). `"9223372036854775808"` for an `int64` passed the schema and the server answered `400 invalid JSON body` without naming the field; `"+5"` was refused by the schema and accepted by the server.
- **The generated TypeScript compiles with `exactOptionalPropertyTypes`** (and the other "strictest" options): optional properties are typed `T | undefined`, which is what Zod's `.optional()` produces, and the client passes `body: null` instead of `undefined` to `fetch`. Three errors before; CI now type-checks with those options too.

## [0.4.0] - 2026-11-18

### Fixed

- **A full `session.MemoryStore`** gives new visitors no session, as documented, instead of answering `500`: the handler's response is sent without a session cookie, and one line a minute is logged instead of one per request. Under a flood of one-time visitors, the `500` made clients retry requests whose work (a queued job, a database write) was already done.
- **`pattern` in the generated schemas** matches the same strings as on the server. The JavaScript regular expressions had no `u` flag, so a pattern with a range of emoji (`[😀-😂]`) made schemas.ts throw a SyntaxError when imported, breaking every schema, and `.`, `[^a]`, `\s`, `\S`, `(?m)` and `(?i)` matched other strings than Go (`"\r"`, an emoji, U+3000, the Kelvin sign...). Patterns are now translated for u-mode JavaScript, including `\pL`, `\p{Greek}`, POSIX classes, `\Q...\E`, octal and `\x{...}` escapes, escaped punctuation and literal braces; the few without a faithful translation are left to the server.
- **Number ranges in the generated schemas**: Zod and OpenAPI now carry the range of `int8`, `int16`, `int32`, the unsigned types and `float32`, which encoding/json enforces. `128` for an `int8` or `-1` for a `uint` passed the Zod schema and came back 400.
- **`required` in the generated schemas** now means what it means on the server: a non-empty string, list or map (`.min(1)` in Zod, `minLength`/`minItems`/`minProperties: 1` in OpenAPI). Zod accepted `""`, `[]` and `{}`, which the server refuses with 422. `min`, `max` and `len` on maps are now checked by Zod too.
- **JSON names follow encoding/json exactly** in OpenAPI, the TypeScript client, the Zod schemas and validation errors. A json tag name with a character encoding/json refuses (`json:"price€"`, a quote, a backslash, an emoji) used to be documented while encoding/json sends the Go field name; `json:"-,"` (a field named `-`) was left out; an embedded struct tagged `json:",omitempty"` was described as one field instead of being flattened, and one tagged `json:"-"` was described although encoding/json leaves it out (its path and query fields are still bound).
- **Two fields with the same JSON name at the same depth** (both tagged or both untagged, for example through two embedded structs) are refused when the route is registered: encoding/json sends and reads neither, while the generators documented one.
- **TypeScript and Zod** string literals are written as JavaScript, not Go: `strconv.Quote` wrote characters above U+FFFF that are not printable as `\U000e0067`, which JavaScript reads as `U000e0067`. A `oneof` with a subdivision flag emoji (🏴󠁧󠁢󠁳󠁣󠁴󠁿) was refused by the generated schema while the server accepted it.
- **Groups** collapse repeated slashes in the prefix and in route paths: `app.Group("/api//")` registered its `"/"` route as the catch-all `/api/`, and `Group("//")` panicked. `"//x"` now joins as `/x`.
- **The request target `*`** (as in `OPTIONS *`) is answered `400`, like `net/http.ServeMux`, instead of `404` or `405`.
- **A client that leaves before the response** is recorded as `499` (`aemeath.StatusClientClosedRequest`), not as a `500` with "internal error: context canceled" in the log: a handler that returns an error wrapping `context.Canceled` is no longer counted as a server error by the Logger, metrics and tracing. `context.DeadlineExceeded` stays a `500`.
- **JSON names on Go 1.27:** encoding/json now runs on encoding/json/v2 by default and reads names in json tags differently (`json:"price€"` is used as is; `json:"we\"ird"` is `we`). The TypeScript client, the Zod schemas, OpenAPI and validation errors follow the rule of the Go version in use (`GOEXPERIMENT=nojsonv2` keeps the old one).
- **Tests on Windows:** the Node tests import zod by `file://` URL, and the `0600` file-mode check of `LocalStorage` runs on Linux and macOS only.
- **CI and the README** fuzz exactly `FuzzBind` (`-fuzz '^FuzzBind$'`); the old pattern also matched `FuzzBindMatchesEncodingJSON`, and `go test` refuses to fuzz two targets.

### Security

- **`LocalStorage.Save`** replaces control characters and invisible formatting characters (newline, tab, NUL, right-to-left override, zero-width space) in the stored name with `_`. `evil\u202Egnp.exe` displayed as `evilexe.png`, and a newline in a name forged lines in logs that print it. Ordinary letters in any script are kept.
- **`CORS`** sends `Vary: Origin` on every response unless `*` is the only allowed origin, also on responses to origins it does not allow and to requests without an Origin header. A shared cache could store one of those and serve it, without CORS headers, to the allowed front end.
- **`ws.Upgrade`** pings every 30 seconds and closes a connection whose client has not answered for 60 (`ws.DefaultIdleTimeout`, `0` to turn it off). net/http's timeouts end when a WebSocket takes the connection over, so a silent client used to hold a connection and a goroutine forever. Browsers answer pings by themselves; if you set your own `PongHandler`, extend the read deadline in it.
- **`csrf.Middleware`** refuses an unsafe request from a visitor that was never given a token before reading the body. It used to parse a form body of up to 32 MiB (spilling to temporary files) first, on any POST route, for any client.
- **Validating a map** turns each key into text once instead of twice per comparison while sorting: a 1 MiB body with 100,000 keys took 615 ms and 118 MB, now 116 ms and 25 MB. Keys that implement `encoding.TextMarshaler` are now named in error paths the way JSON writes them.
- **Validation** stops after `aemeath.MaxValidationErrors` errors (100 by default, `0` for no limit). A 1 MiB JSON body of empty objects used to produce 349,517 errors and a 25 MB response.
- **`Static` and `Ctx.File`** send `X-Content-Type-Options: nosniff` on every file, and a file whose extension has no known type ("note", "report.dat") as `application/octet-stream` instead of a type guessed from its content. An uploaded file that started with `<!doctype html>` used to be served as `text/html` (stored XSS) even though `LocalStorage` renames `.html`, `.svg` and `.js` uploads.
- **Server-Sent Events** get a write timeout per event (`aemeath.DefaultSSEWriteTimeout`, 10 seconds; `SSE.SetWriteTimeout` changes it, `0` turns it off). A client that stopped reading but kept the connection open used to block the handler in `Send` forever.
- **Request bodies are capped by default (breaking):** every body, including one a handler reads itself from `c.R.Body`, stops at `aemeath.DefaultMaxRequestBytes` (by default the larger of the JSON and multipart limits, 32 MiB), and returning the read error answers `413`. A raw handler used to read any amount (300 MB in the test). For larger uploads put `MaxBodySize(n)` on the group; it now replaces the default cap instead of nesting inside it, and `MaxBodySize(-1)` removes it.
- **Request headers are limited to 64 KiB (breaking)** by `NewServer` (`aemeath.DefaultMaxHeaderBytes`; net/http's default is 1 MiB). Larger headers get `431`. 200 clients sending 1000 KB of headers each took the heap from 51-86 MB down to 7-15 MB.

### Documentation

- The README's performance section now separates the one-machine timings from the allocation counts, which are measured by `BenchmarkHelloRoute` and `BenchmarkValidatedJSONPost` (16 and 32 allocations with Go 1.27, the version in `go.mod`; the test that checks them is skipped on other Go versions, whose counts differ).
- Corrected: the OpenAPI `<Type>Input` components (an operation's own input struct is inlined), the TypeScript types of nil slices and maps, the per-hook shutdown limit, what `jwt.Middleware` and `RateLimiter` actually enforce in `SECURITY.md`, and the comparison page, which still said there was no job runner and no metrics or tracing.
- The README has a section on behaviour under pressure (shutdown, slow clients, no connection limit, `Timeout` and handlers that ignore their context), each point tested in `internal/hostile`.
- `internal/docclaims` tests the statements made in the README and `docs/` against the running framework, so a claim that stops being true fails the build.

### Performance

- A JSON body is decoded a second time (to learn which keys were sent) only when a validated value is empty, instead of on every request with validation rules. A body whose validated values are filled in costs 35 allocations instead of 46 for two fields (measured with Go 1.24), and a 200-item list about 40% less time and memory. Validation results are unchanged; a test and a fuzz target compare both ways.

### Added

- `App.SetShutdownTimeout(d)`: how long requests in flight get to finish after SIGINT/SIGTERM (`aemeath.DefaultShutdownTimeout`, 10 seconds, which used to be fixed). When it runs out their connections are closed and their clients get no response.

### Changed

- **Validation (breaking):** `email`, `url` and `uuid` are now defined once and used by the server, the Zod schemas and OpenAPI alike. `email` requires a dot in the domain (`a@b` and `a@[127.0.0.1]` are refused; non-ASCII addresses are still accepted). The Zod schemas no longer accept non-http(s) URLs such as `javascript:` or `ftp:`.
- **Validation (breaking):** in a JSON body, a key that is present is now validated even when its value is empty. `{"website": ""}` fails `url`, `{"tags": []}` fails `min=1` and `{"kind": 0}` fails `oneof=1 2`; before, rules were skipped for every empty value. Leave the key out or send `null` for "no value". Forms and query strings are unchanged (an empty value is not sent). This matches the generated Zod schemas.

## [0.3.1] - 2026-11-14

### Fixed

- **`session.MemoryStore`** no longer grows without bound: expired sessions are swept automatically and live sessions are capped at 100,000 (`WithMaxSessions` changes it). When full, new visitors get no session until older ones expire; existing sessions are unaffected.
- **`/docs`** now loads the Scalar viewer from a pinned version instead of the latest release.

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
