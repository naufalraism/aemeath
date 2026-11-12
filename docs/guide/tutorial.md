# Tutorial: a notes API with login, uploads and metrics

You will build a small but realistic backend: sign up and log in with a **session**, protect it from **CSRF**, create notes with an optional **image upload**, and watch it with **metrics**. It needs no database; the finished program is [`examples/notes`](https://github.com/naufalraism/aemeath/tree/main/examples/notes) and its tests run in CI, so every step below is known to work.

You need Go 1.27 or newer.

## 1. Start the project

```bash
mkdir notes && cd notes
go mod init notes
go get github.com/naufalraism/aemeath@latest
```

## 2. A route with validation

Input is a struct. Tags say where each field comes from and what is allowed; the same tags generate the OpenAPI document and the TypeScript client.

```go
type credentials struct {
	Name     string `json:"name" validate:"required,min=3,max=20"`
	Password string `json:"password" validate:"required,min=8"`
}

type meOut struct {
	Name string `json:"name"`
}

app := aemeath.New()
aemeath.POST(app, "/api/signup", func(c *aemeath.Ctx, in credentials) (meOut, error) {
	return meOut{Name: in.Name}, nil
}, aemeath.Status(http.StatusCreated))
```

Send a 2-character name and you get **422** with a per-field error list, without writing any checking code:

```json
{"error":"validation failed","errors":[{"field":"name","rule":"min","message":"must be at least 3 characters"}]}
```

## 3. Sessions

Add the session middleware, then renew the session id at login:

```go
app.Use(session.Middleware(session.Config{Store: session.NewMemoryStore()}))

login := func(c *aemeath.Ctx, name string) error {
	s := session.From(c)
	if err := s.Renew(c.Context()); err != nil { // a new id at login: no session fixation
		return err
	}
	s.Set("user", name)
	return nil
}
```

The browser only ever holds a random id in an `HttpOnly` cookie. Secure cookies are the default; run the example with `DEV=1` so they work on `http://localhost`.

## 4. CSRF

```go
app.Use(csrf.Middleware(csrf.Config{}))

aemeath.GET(app, "/api/csrf", func(c *aemeath.Ctx, _ empty) (tokenOut, error) {
	return tokenOut{Token: csrf.Token(c)}, nil
})
```

Your front end calls `/api/csrf` once after login and sends the token as `X-CSRF-Token` on every state-changing request. A forged request from another site cannot read it, and gets 403.

## 5. Private data

A handler decides who the user is from the session, never from the request body:

```go
current := func(c *aemeath.Ctx) (string, error) {
	u := session.From(c).String("user")
	if u == "" {
		return "", aemeath.Unauthorized("log in first")
	}
	return u, nil
}
```

When Bob asks for Ann's note, answer **404**, exactly as for a note that does not exist. Answering 403 would tell him the note is there.

## 6. A file upload

```go
type createNote struct {
	Title  string                `form:"title" validate:"required,max=100"`
	Body   string                `form:"body" validate:"max=5000"`
	Attach *multipart.FileHeader `form:"attachment" accept:"image/png,image/jpeg" maxsize:"2MB"`
}
```

`accept` inspects the file's **bytes**, so a script renamed to `.png` is rejected. See [Forms and file uploads](./forms).

## 7. Metrics and health

```go
reg := metrics.New()
app.Use(reg.Middleware())
app.Handle("GET", "/metrics", reg.Handler())
app.EnableHealth()
```

Point Prometheus at `/metrics` (on an internal port in production) and your load balancer at `/readyz`.

## 8. Run it and call it

```bash
DEV=1 go run .
```

```bash
# cookies are kept in jar.txt; first the CSRF token, then sign up
TOKEN=$(curl -s -c jar.txt localhost:8080/api/csrf | jq -r .csrf_token)
curl -s -b jar.txt -c jar.txt -H "X-CSRF-Token: $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"ann","password":"long-enough"}' localhost:8080/api/signup
# the session id changed at signup, so fetch a fresh token
TOKEN=$(curl -s -b jar.txt -c jar.txt localhost:8080/api/csrf | jq -r .csrf_token)
curl -s -b jar.txt -H "X-CSRF-Token: $TOKEN" -F title=Hello -F attachment=@photo.png localhost:8080/api/notes
```

## 9. Test it without a network

The example's `main_test.go` drives the whole flow with `httptest`: sign up, create, forbid another user, reject a fake image, forged CSRF, logout. Copy that pattern for your own app.

## 10. A typed front-end client

```bash
aemeath ts --out web/src/api      # with the CLI, or: AEMEATH_TS_OUT=web/src/api go run .
```

You get `client.ts`, `schemas.ts` (Zod) and `hooks.ts` (TanStack Query), generated from the same structs, so a renamed field breaks the front end at compile time instead of in production.

## Where next

- Swap the in-memory store for [PostgreSQL](./database) and sessions for [Redis](./auth#redis-store-several-servers-survives-restarts).
- Hash passwords with `argon2` or `bcrypt` from `golang.org/x/crypto`. The example stores them as plain text only to stay short; never do that.
- Add [OpenID Connect](./auth#openid-connect) so users can log in with Google.
- Add [tracing](./observability#tracing-w3c-trace-context-otlp).
