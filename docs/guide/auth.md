# Authentication

Aemeath does not choose for you. Pick what fits the client:

| Client | Use |
| :--- | :--- |
| Browser app on your own domain | **Sessions** + **CSRF** |
| Mobile app, other servers, third-party API users | **JWT** (shared secret or public key) |
| "Log in with Google/Microsoft/Okta/Keycloak" | **OpenID Connect**, which ends in a session |

## Sessions

A session is data kept on the server. The browser only holds a random 43-character id in an `HttpOnly`, `Secure`, `SameSite=Lax` cookie.

```go
import "github.com/naufalraism/aemeath/session"

app.Use(session.Middleware(session.Config{
	Store: session.NewMemoryStore(), // development; use Redis in production (below)
	TTL:   24 * time.Hour,
}))

aemeath.POST(app, "/login", func(c *aemeath.Ctx, in LoginIn) (Me, error) {
	// ... check the password ...
	s := session.From(c)
	if err := s.Renew(c.Context()); err != nil { // ALWAYS renew at login
		return Me{}, err
	}
	s.Set("user", in.Name)
	return Me{Name: in.Name}, nil
})

aemeath.POST(app, "/logout", func(c *aemeath.Ctx, _ struct{}) (struct{}, error) {
	session.From(c).Destroy()
	return struct{}{}, nil
})
```

`MemoryStore` sweeps expired sessions by itself and keeps at most 100,000 live sessions (`WithMaxSessions(n)` changes it). When it is full, new visitors get no session until older ones expire: they receive the handler's response without a session cookie (not an error, so clients do not repeat work that was already done), and one line a minute is logged. Existing sessions keep working; a `Renew` in that time loses its session. It lives in one process and is lost on restart, so use the Redis store when you run more than one instance.

Every store keeps values as JSON, `MemoryStore` included, so what works on your laptop works on Redis: on the next request a number comes back as `float64`, a struct as `map[string]any`, a slice as `[]any`. Read numbers with `s.Int("user_id")` (it takes any integer type and a whole `float64`) rather than `s.Get("user_id").(int)`, which only works in the request that set the value. A value JSON cannot encode (a channel, a function) is refused when the session is saved, with every store.

If saving fails for another reason (Redis is down), the request is answered `500`, because a "logged in" response without a stored session would be a lie. The handler has run by then: write handlers whose work may be repeated safely, or check the store before work that must not.

**Requests of one session that run at the same time** (two tabs, polling, an upload in progress) save only the keys they changed, on top of what the store holds when they finish. A slow request that sets `cart` keeps the `theme` a faster one saved meanwhile, and a slow request that ends after a logout does not bring the session back. When two requests change **the same key**, the last one to finish wins: two tabs adding to a counter at the same moment can still lose an increment. Keep values that many parallel requests update (counters, carts with heavy traffic) in your database, where an update can be atomic. The reload and the save are two calls to the store, so requests finishing within the same moment can still overlap; the window is that short, not the length of a request.


Safety properties, each covered by a test:

- An id the server did not issue is **never adopted**, so an attacker cannot plant a known id in a victim's browser (session fixation).
- `Renew` issues a new id **and deletes the old one**. Call it whenever the user's privilege changes (login, password change).
- A cookie is only sent when the session was touched, so anonymous visitors get none.
- A session ended by `Destroy` or `Renew` stays ended: a request of the same visitor that was still running does not store it again.
- `Config.Insecure` exists for `http://localhost` development only. The default is `Secure`.

### Redis store (several servers, survives restarts)

```go
cachePlugin := cache.New(cache.Config{Addr: "localhost:6379"})
app.RegisterPlugin(cachePlugin)
app.Use(session.Middleware(session.Config{Store: cache.NewSessionStore(cachePlugin, "")}))
```

A managed Redis (Upstash, Redis Cloud and the like) gives you a URL; `rediss://` means TLS, which these services require: `cache.New(cache.Config{URL: os.Getenv("REDIS_URL")})`. A malformed URL stops the server at startup.

Values are stored as JSON. Numbers come back as `float64`; `s.Int("n")` handles that. A Redis outage returns an error (HTTP 500) instead of silently logging everyone out.

## CSRF

Cookies are sent by the browser even when a hostile page triggers the request. `csrf.Middleware` blocks that.

```go
import "github.com/naufalraism/aemeath/csrf"

app.Use(session.Middleware(/* ... */), csrf.Middleware(csrf.Config{}))

aemeath.GET(app, "/api/csrf", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
	return map[string]string{"token": csrf.Token(c)}, nil
})
```

The front end fetches the token once and sends it as `X-CSRF-Token` on every `POST`, `PUT`, `PATCH` and `DELETE`. Requests with a missing or wrong token get 403. If the request has an `Origin` header, it must be same-origin or listed in `TrustedOrigins`. Fetch a new token after login, because the session id (and token) change.

For a plain HTML form, which cannot send headers, put the token in a hidden field named `csrf_token` (change it with `FormField`). Both `application/x-www-form-urlencoded` and `multipart/form-data` bodies work, and the route still binds the rest of the form:

```html
<form method="post" action="/notes">
  <input type="hidden" name="csrf_token" value="{{ .CSRFToken }}">
  <input name="title">
</form>
```

The token is only created when something asks for it (`csrf.Token`), so visitors who never do, such as crawlers, get no session.

Set `Cookie: "XSRF-TOKEN"` to also publish the token in a readable cookie for front-end libraries that read it automatically. That creates a session for every visitor who loads a page, so use a store that can hold them (Redis) rather than `MemoryStore`.

`session.Middleware` already refuses cross-origin writes on its own, without a token: a `POST`, `PUT`, `PATCH` or `DELETE` that the browser marks as coming from another site (`Sec-Fetch-Site`, or `Origin` on browsers that do not send it) gets 403. Your own pages, reads, and clients that are not browsers (curl, mobile apps, webhooks) are not affected. A front end on another origin, such as `https://app.example.com` calling `https://api.example.com` with cookies, must be listed:

```go
session.Config{Store: store, TrustedOrigins: []string{"https://app.example.com"}}
```

When you also use `csrf.Middleware` with `TrustedOrigins`, list the same origins in both. `csrf.Middleware` adds the token check on top, which also covers older browsers that send neither header. `AllowCrossOrigin: true` turns the session check off; use it only when every write is protected in another way.

CSRF only matters for cookie authentication. Pure Bearer-token APIs do not need it.

## JWT

```go
import "github.com/naufalraism/aemeath/jwt"

// 1. A shared secret (HS256/384/512). Fine when one server issues and verifies.
//    At least 32 random bytes, e.g. `openssl rand -base64 32`; shorter panics at startup.
app.Use(jwt.Middleware(jwt.Config{SecretKey: os.Getenv("JWT_SECRET")}))

// 2. A public key (RS256, ES256, EdDSA...). The verifier cannot mint tokens.
app.Use(jwt.Middleware(jwt.Config{PublicKey: rsaPublicKey, Issuer: "https://auth.example.com", Audience: "my-api"}))

// 3. A provider's published keys (Auth0, Keycloak, Cognito, Okta...).
app.Use(jwt.Middleware(jwt.Config{JWKSURL: "https://example.auth0.com/.well-known/jwks.json", Audience: "my-api"}))
```

Exactly one key source must be set; a missing or empty secret makes the server refuse to start, because it would otherwise accept forged tokens.

What it enforces:

- Only algorithms that fit the key are accepted. A token cannot be "signed" with an RSA public key used as an HMAC secret (algorithm confusion), and `alg: none` is rejected.
- `exp` is required unless you set `AllowNoExpiry`. `Issuer`, `Audience` and `Leeway` are checked when set.
- JWKS keys are cached, refreshed after a key rotation, and refreshes are rate limited, so a flood of tokens with random `kid` values cannot make your server hammer the provider. If the provider is down, cached keys keep working. The URL must be `https`.
- RSA keys under 2048 bits are rejected.

Read the verified claims with `jwt.Claims(c)` and `jwt.UserID(c)`. Create tokens with `jwt.Issue("RS256", privateKey, jwt.IssueOptions{Subject: "42", TTL: 15 * time.Minute, KeyID: "2026-01"})`; `kid` lets verifiers pick the right key after a rotation.

## OpenID Connect

`aemeath/oidc` implements the authorization code flow with PKCE and nonce.

```go
provider, err := oidc.Discover(ctx, oidc.Config{
	Issuer:       "https://accounts.google.com",
	ClientID:     os.Getenv("OIDC_CLIENT_ID"),
	ClientSecret: os.Getenv("OIDC_CLIENT_SECRET"),
	RedirectURL:  "https://app.example.com/auth/callback",
})
if err != nil {
	log.Fatal(err)
}

app.Handle("GET", "/auth/login", func(c *aemeath.Ctx) error { return provider.Begin(c) })
app.Handle("GET", "/auth/callback", func(c *aemeath.Ctx) error {
	id, err := provider.Complete(c)
	if err != nil {
		return err // always a 401
	}
	s := session.From(c)
	if err := s.Renew(c.Context()); err != nil {
		return err
	}
	s.Set("sub", id.Issuer+"|"+id.Subject)
	return c.Redirect(http.StatusFound, "/")
})
```

`Begin` and `Complete` need `session.Middleware`: the state, nonce and PKCE verifier live in the server-side session, never in a cookie the browser can edit. For a visitor who has no session yet, `Begin` creates one that lasts `MaxLoginAge` (10 minutes) instead of the session TTL, so crawlers following the login link cannot keep the session store full; after the login it is saved with the usual TTL. `Complete` checks:

- the `state` (constant time), and that a login attempt can only be completed **once**, even after a failure;
- the PKCE verifier (the token endpoint rejects a stolen code);
- the ID token's signature through the provider's JWKS, plus issuer, audience, expiry, and the nonce;
- several audiences require `azp` to be your client id.

`Discover` fails closed: the issuer in the discovery document must equal yours exactly (mix-up attacks), every endpoint must be `https`, and a provider that advertises PKCE methods must include `S256`.

Identify users by **issuer + subject**. An e-mail address can be reassigned and is only as trustworthy as the provider's `email_verified`.

## Plain OAuth2

For providers without OpenID Connect (GitHub, for example), use `aemeath/oauth2`. Prefer OpenID Connect whenever the provider offers it: it also verifies an ID token.

```go
github := oauth2.New("github", &xoauth2.Config{
	ClientID:     os.Getenv("GITHUB_CLIENT_ID"),
	ClientSecret: os.Getenv("GITHUB_CLIENT_SECRET"),
	RedirectURL:  "https://app.example.com/auth/github/callback",
	Scopes:       []string{"read:user"},
	Endpoint:     githuboauth.Endpoint, // golang.org/x/oauth2/github
}, "https://api.github.com/user")

app.Handle("GET", "/auth/github/login", func(c *aemeath.Ctx) error { return github.Begin(c) })
app.Handle("GET", "/auth/github/callback", func(c *aemeath.Ctx) error {
	login, err := github.Complete(c) // 401 if the state is wrong, reused or too old
	if err != nil {
		return err
	}
	s := session.From(c)
	if err := s.Renew(c.Context()); err != nil {
		return err
	}
	s.Set("github_id", login.User["id"])
	return c.Redirect(302, "/")
})
```

Like `oidc`, it needs `session.Middleware`: `Begin` keeps the state in the server-side session and `Complete` checks it once, within `MaxLoginAge` (10 minutes). Set `PKCE: true` on the plugin when the provider supports PKCE. Identify users by the provider's stable id (`id` on GitHub), not by login name or e-mail, which can change hands. `AuthURL`, `Exchange` and `UserInfo` remain for apps that keep the state themselves.
