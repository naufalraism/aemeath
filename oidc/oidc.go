// Package oidc implements "Log in with ..." through OpenID Connect providers
// (Google, Microsoft, Auth0, Keycloak, Okta, ...) using the authorization code
// flow with PKCE.
//
//	p, err := oidc.Discover(ctx, oidc.Config{
//		Issuer: "https://accounts.google.com", ClientID: "...", ClientSecret: "...",
//		RedirectURL: "https://app.example.com/auth/callback",
//	})
//	app.Handle("GET", "/auth/login", func(c *aemeath.Ctx) error { return p.Begin(c) })
//	app.Handle("GET", "/auth/callback", func(c *aemeath.Ctx) error {
//		id, err := p.Complete(c)
//		if err != nil { return err }
//		s := session.From(c)
//		if err := s.Renew(c.Context()); err != nil { return err }
//		s.Set("sub", id.Issuer+"|"+id.Subject)
//		return c.Redirect(302, "/")
//	})
//
// It needs session.Middleware: the state, nonce and PKCE verifier live in the
// server-side session, never in a cookie the browser could tamper with.
//
// Identify users by Issuer plus Subject. An e-mail address can change hands and
// is only as trustworthy as the provider's email_verified claim.
package oidc

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
	xoauth2 "golang.org/x/oauth2"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/jwt"
	"github.com/naufalraism/aemeath/session"
)

// Config describes the relying party (your app) at one provider.
type Config struct {
	Issuer       string // the provider's issuer URL, exactly as it appears in its ID tokens
	ClientID     string
	ClientSecret string   // empty for a public client
	RedirectURL  string   // your callback URL, registered at the provider
	Scopes       []string // "openid" is always included; default adds "profile" and "email"
	HTTPClient   *http.Client
	// MaxLoginAge limits how long a login may take between Begin and Complete (default 10 minutes).
	MaxLoginAge time.Duration
	// Leeway tolerates clock skew when checking exp/iat (default 1 minute).
	Leeway time.Duration
}

// Provider is a discovered OpenID Connect provider.
type Provider struct {
	cfg      Config
	oauth    *xoauth2.Config
	verifier *jwt.Verifier
	endpoint struct{ Auth, Token, UserInfo string }
}

// Identity is a verified login.
type Identity struct {
	Issuer        string
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Claims        map[string]any // every claim of the verified ID token
	Token         *xoauth2.Token // access and refresh tokens, if you need to call the provider's APIs
}

type discovery struct {
	Issuer   string   `json:"issuer"`
	AuthURL  string   `json:"authorization_endpoint"`
	TokenURL string   `json:"token_endpoint"`
	JWKSURI  string   `json:"jwks_uri"`
	UserInfo string   `json:"userinfo_endpoint"`
	Methods  []string `json:"code_challenge_methods_supported"`
}

func isLoopback(u *url.URL) bool {
	h := u.Hostname()
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// secureURL accepts https URLs, and http only for loopback (tests and local development).
func secureURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid URL %q", raw)
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !isLoopback(u)) {
		return nil, fmt.Errorf("URL must use https: %q", raw)
	}
	return u, nil
}

// Discover reads the provider's /.well-known/openid-configuration and prepares
// a Provider. It fails closed: the issuer in the document must equal
// Config.Issuer exactly (defeating mix-up attacks), every endpoint must be
// https, and the provider must support PKCE with S256 when it advertises its methods.
func Discover(ctx context.Context, cfg Config) (*Provider, error) {
	if cfg.ClientID == "" || cfg.RedirectURL == "" {
		return nil, errors.New("aemeath/oidc: ClientID and RedirectURL are required")
	}
	iss, err := secureURL(cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("aemeath/oidc: issuer: %w", err)
	}
	if _, err := secureURL(cfg.RedirectURL); err != nil {
		return nil, fmt.Errorf("aemeath/oidc: redirect URL: %w", err)
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(iss.String(), "/")+"/.well-known/openid-configuration", nil)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("aemeath/oidc: discovery failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("aemeath/oidc: discovery returned status %d", resp.StatusCode)
	}
	var d discovery
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&d); err != nil {
		return nil, fmt.Errorf("aemeath/oidc: discovery document: %w", err)
	}
	if d.Issuer != cfg.Issuer {
		return nil, fmt.Errorf("aemeath/oidc: the provider reports issuer %q, expected %q", d.Issuer, cfg.Issuer)
	}
	for name, raw := range map[string]string{"authorization_endpoint": d.AuthURL, "token_endpoint": d.TokenURL, "jwks_uri": d.JWKSURI} {
		if _, err := secureURL(raw); err != nil {
			return nil, fmt.Errorf("aemeath/oidc: %s: %w", name, err)
		}
	}
	if len(d.Methods) > 0 {
		ok := false
		for _, m := range d.Methods {
			ok = ok || m == "S256"
		}
		if !ok {
			return nil, errors.New("aemeath/oidc: the provider does not support PKCE with S256")
		}
	}
	jwks, err := jwt.NewJWKS(d.JWKSURI, jwt.WithJWKSHTTPClient(hc))
	if err != nil {
		return nil, fmt.Errorf("aemeath/oidc: %w", err)
	}
	if cfg.Leeway <= 0 {
		cfg.Leeway = time.Minute
	}
	if cfg.MaxLoginAge <= 0 {
		cfg.MaxLoginAge = 10 * time.Minute
	}
	scopes := append([]string{"openid"}, cfg.Scopes...)
	if len(cfg.Scopes) == 0 {
		scopes = []string{"openid", "profile", "email"}
	}
	p := &Provider{cfg: cfg}
	p.endpoint.Auth, p.endpoint.Token, p.endpoint.UserInfo = d.AuthURL, d.TokenURL, d.UserInfo
	p.oauth = &xoauth2.Config{
		ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, RedirectURL: cfg.RedirectURL, Scopes: dedupe(scopes),
		Endpoint: xoauth2.Endpoint{AuthURL: d.AuthURL, TokenURL: d.TokenURL},
	}
	p.verifier = jwt.NewVerifier(jwt.Config{JWKS: jwks, Issuer: cfg.Issuer, Audience: cfg.ClientID, Leeway: cfg.Leeway})
	return p, nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func random(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("oidc: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

const sessionKey = "aemeath.oidc"

type pending struct {
	State, Nonce, Verifier string
	Started                int64
}

// Begin starts a login: it stores a fresh state, nonce and PKCE verifier in the
// session and redirects the browser to the provider.
func (p *Provider) Begin(c *aemeath.Ctx) error {
	st := pending{State: random(24), Nonce: random(24), Verifier: xoauth2.GenerateVerifier(), Started: time.Now().Unix()}
	s := session.From(c)
	s.Set(sessionKey, map[string]any{"s": st.State, "n": st.Nonce, "v": st.Verifier, "t": st.Started})
	u := p.oauth.AuthCodeURL(st.State, xoauth2.S256ChallengeOption(st.Verifier), xoauth2.SetAuthURLParam("nonce", st.Nonce))
	return c.Redirect(http.StatusFound, u)
}

// ErrLogin is the suffix of every Complete failure that means "this login attempt is not valid" (always a 401).
var ErrLogin = errors.New("login failed")

func fail(format string, a ...any) error {
	return aemeath.NewError(http.StatusUnauthorized, "aemeath/oidc: "+fmt.Sprintf(format, a...)+": "+ErrLogin.Error())
}

// Complete finishes the login on the callback request. It checks the state
// (constant time), exchanges the code with the PKCE verifier, verifies the ID
// token (signature through the provider's JWKS, issuer, audience, expiry and
// the nonce) and returns the Identity. The login can only be completed once.
func (p *Provider) Complete(c *aemeath.Ctx) (*Identity, error) {
	s := session.From(c)
	raw, _ := s.Get(sessionKey)
	s.Delete(sessionKey) // one attempt per Begin, whatever happens next
	m, _ := raw.(map[string]any)
	state, _ := m["s"].(string)
	nonce, _ := m["n"].(string)
	verifier, _ := m["v"].(string)
	started, _ := m["t"].(float64)
	if t, ok := m["t"].(int64); ok {
		started = float64(t)
	}
	if state == "" || nonce == "" || verifier == "" {
		return nil, fail("no login in progress")
	}
	if time.Since(time.Unix(int64(started), 0)) > p.cfg.MaxLoginAge {
		return nil, fail("the login took too long")
	}
	q := c.R.URL.Query()
	if e := q.Get("error"); e != "" {
		return nil, fail("the provider returned an error (%s)", sanitize(e))
	}
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
		return nil, fail("state mismatch")
	}
	code := q.Get("code")
	if code == "" {
		return nil, fail("missing code")
	}
	ctx := context.WithValue(c.Context(), xoauth2.HTTPClient, p.httpClient())
	tok, err := p.oauth.Exchange(ctx, code, xoauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fail("code exchange failed")
	}
	idt, _ := tok.Extra("id_token").(string)
	if idt == "" {
		return nil, fail("the provider returned no id_token")
	}
	claims, err := p.verifier.Verify(idt)
	if err != nil {
		return nil, fail("invalid id_token")
	}
	return p.identity(claims, nonce, tok)
}

func (p *Provider) httpClient() *http.Client {
	if p.cfg.HTTPClient != nil {
		return p.cfg.HTTPClient
	}
	return http.DefaultClient
}

func (p *Provider) identity(claims gojwt.MapClaims, nonce string, tok *xoauth2.Token) (*Identity, error) {
	if n, _ := claims["nonce"].(string); subtle.ConstantTimeCompare([]byte(n), []byte(nonce)) != 1 {
		return nil, fail("nonce mismatch")
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return nil, fail("id_token has no subject")
	}
	// With several audiences the token must name us as the authorized party.
	if aud, ok := claims["aud"].([]any); ok && len(aud) > 1 {
		if azp, _ := claims["azp"].(string); azp != p.cfg.ClientID {
			return nil, fail("id_token has several audiences and azp is not this client")
		}
	}
	id := &Identity{Issuer: p.cfg.Issuer, Subject: sub, Claims: map[string]any(claims), Token: tok}
	id.Email, _ = claims["email"].(string)
	id.Name, _ = claims["name"].(string)
	switch v := claims["email_verified"].(type) {
	case bool:
		id.EmailVerified = v
	case string: // some providers send "true"
		id.EmailVerified = v == "true"
	}
	return id, nil
}

func sanitize(s string) string {
	if len(s) > 64 {
		s = s[:64]
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e {
			return '?'
		}
		return r
	}, s)
}
