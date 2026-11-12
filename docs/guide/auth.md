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

Safety properties, each covered by a test:

- An id the server did not issue is **never adopted**, so an attacker cannot plant a known id in a victim's browser (session fixation).
- `Renew` issues a new id **and deletes the old one**. Call it whenever the user's privilege changes (login, password change).
- A cookie is only sent when the session was touched, so anonymous visitors get none.
- `Config.Insecure` exists for `http://localhost` development only. The default is `Secure`.

### Redis store (several servers, survives restarts)

```go
cachePlugin := cache.New("localhost:6379", "", 0)
app.RegisterPlugin(cachePlugin)
app.Use(session.Middleware(session.Config{Store: cache.NewSessionStore(cachePlugin, "")}))
```

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

Set `Cookie: "XSRF-TOKEN"` to also publish the token in a readable cookie for front-end libraries that read it automatically.

CSRF only matters for cookie authentication. Pure Bearer-token APIs do not need it.

## JWT

```go
import "github.com/naufalraism/aemeath/jwt"

// 1. A shared secret (HS256/384/512). Fine when one server issues and verifies.
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

`Begin` and `Complete` need `session.Middleware`: the state, nonce and PKCE verifier live in the server-side session, never in a cookie the browser can edit. `Complete` checks:

- the `state` (constant time), and that a login attempt can only be completed **once**, even after a failure;
- the PKCE verifier (the token endpoint rejects a stolen code);
- the ID token's signature through the provider's JWKS, plus issuer, audience, expiry, and the nonce;
- several audiences require `azp` to be your client id.

`Discover` fails closed: the issuer in the discovery document must equal yours exactly (mix-up attacks), every endpoint must be `https`, and a provider that advertises PKCE methods must include `S256`.

Identify users by **issuer + subject**. An e-mail address can be reassigned and is only as trustworthy as the provider's `email_verified`.

## Plain OAuth2

For providers without OpenID Connect (GitHub, for example), `aemeath/oauth2` handles the code exchange and user-info fetch. Prefer OpenID Connect whenever the provider offers it.
