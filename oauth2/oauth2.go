// Package oauth2 handles login through OAuth2 providers that do not speak
// OpenID Connect, such as GitHub. For providers that do (Google, Microsoft,
// Auth0, Keycloak, Okta, ...) use package oidc, which also verifies an ID
// token.
//
//	gh := oauth2.New("github", &xoauth2.Config{
//		ClientID: "...", ClientSecret: "...",
//		RedirectURL: "https://app.example.com/auth/github/callback",
//		Scopes: []string{"read:user", "user:email"},
//		Endpoint: github.Endpoint, // golang.org/x/oauth2/github
//	}, "https://api.github.com/user")
//	app.Handle("GET", "/auth/github", func(c *aemeath.Ctx) error { return gh.Begin(c) })
//	app.Handle("GET", "/auth/github/callback", func(c *aemeath.Ctx) error {
//		login, err := gh.Complete(c)
//		if err != nil { return err }
//		session.From(c).Set("github_id", login.User["id"]) // Complete renewed the id
//		return c.Redirect(302, "/")
//	})
//
// Begin and Complete need session.Middleware: the state lives in the
// server-side session, and Complete checks it, so a forged callback cannot
// log a victim into the attacker's account.
package oauth2

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
	"strings"
	"time"

	xoauth2 "golang.org/x/oauth2"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/session"
)

// Plugin is one OAuth2 provider.
type Plugin struct {
	ProviderName string
	Config       *xoauth2.Config
	UserInfoURL  string

	// PKCE sends a code challenge in Begin and the verifier in Complete
	// (RFC 7636). Turn it on when the provider supports PKCE; it is off by
	// default because a provider that does not may refuse the exchange.
	PKCE bool
	// MaxLoginAge limits how long a login may take between Begin and
	// Complete (default 10 minutes).
	MaxLoginAge time.Duration
	// HTTPClient is used for the code exchange and the user info request
	// (default: a client with a 10-second timeout).
	HTTPClient *http.Client
}

// New creates a Plugin for a provider.
func New(providerName string, config *xoauth2.Config, userInfoURL string) *Plugin {
	return &Plugin{ProviderName: providerName, Config: config, UserInfoURL: userInfoURL}
}

// registry holds every provider registered on one application.
type registry struct{ providers map[string]*Plugin }

// Apply registers the provider; look it up in a handler with From(c, name).
func (p *Plugin) Apply(app *aemeath.App) error {
	reg, ok := aemeath.Lookup[*registry](app)
	if !ok {
		reg = &registry{providers: map[string]*Plugin{}}
		aemeath.Provide(app, reg)
	}
	if _, dup := reg.providers[p.ProviderName]; dup {
		return fmt.Errorf("aemeath/oauth2: provider %q is already registered", p.ProviderName)
	}
	reg.providers[p.ProviderName] = p
	return nil
}

// From returns the provider registered under name.
func From(c *aemeath.Ctx, name string) (*Plugin, bool) {
	reg, ok := aemeath.Get[*registry](c)
	if !ok {
		return nil, false
	}
	p, ok := reg.providers[name]
	return p, ok
}

// GenerateState returns a random, URL-safe value for the OAuth2 "state"
// parameter. Store it (for example in a short-lived cookie) before redirecting
// and compare it in the callback to prevent CSRF.
//
// Deprecated: Begin and Complete create, store and check the state for you.
// GenerateState will be removed before v1.0.
func GenerateState() (string, error) { return newState() }

func newState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("aemeath/oauth2: failed to generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// AuthURL returns the URL that sends the user to the provider's login page.
// Begin uses it and also stores and later checks the state; call AuthURL
// directly only if you keep the state yourself.
func (p *Plugin) AuthURL(state string) string { return p.Config.AuthCodeURL(state) }

// Exchange trades the authorization code (from the callback) for an access token.
func (p *Plugin) Exchange(ctx context.Context, code string) (*xoauth2.Token, error) {
	return p.Config.Exchange(ctx, code)
}

// UserInfo fetches the user's profile from the provider using the access token.
func (p *Plugin) UserInfo(ctx context.Context, token *xoauth2.Token) (map[string]any, error) {
	resp, err := p.Config.Client(ctx, token).Get(p.UserInfoURL)
	if err != nil {
		return nil, fmt.Errorf("aemeath/oauth2: failed to request user info: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("aemeath/oauth2: provider returned status %d", resp.StatusCode)
	}
	var info map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info); err != nil {
		return nil, fmt.Errorf("aemeath/oauth2: failed to decode user info: %w", err)
	}
	return info, nil
}

const defaultMaxLoginAge = 10 * time.Minute

func (p *Plugin) sessionKey() string { return "aemeath.oauth2." + p.ProviderName }

func (p *Plugin) maxLoginAge() time.Duration {
	if p.MaxLoginAge > 0 {
		return p.MaxLoginAge
	}
	return defaultMaxLoginAge
}

func (p *Plugin) httpClient() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// errNoSession explains a missing session.Middleware instead of the panic
// session.From raises.
var errNoSession = errors.New("aemeath/oauth2: Begin and Complete need session.Middleware installed before the route")

func sessionOf(c *aemeath.Ctx) (s *session.Session, err error) {
	defer func() {
		if recover() != nil {
			s, err = nil, errNoSession
		}
	}()
	return session.From(c), nil
}

// Begin starts a login: it stores a new random state (and, with PKCE, a code
// verifier) in the session and redirects to the provider's login page.
func (p *Plugin) Begin(c *aemeath.Ctx) error {
	s, err := sessionOf(c)
	if err != nil {
		return err
	}
	state, err := newState()
	if err != nil {
		return err
	}
	pending := map[string]any{"s": state, "t": time.Now().Unix()}
	var opts []xoauth2.AuthCodeOption
	if p.PKCE {
		v := xoauth2.GenerateVerifier()
		pending["v"] = v
		opts = append(opts, xoauth2.S256ChallengeOption(v))
	}
	if s.IsNew() {
		// A visitor without a session gets one that lasts as long as the
		// login may take, not the session TTL: a crawler requesting the login
		// page must not fill the store.
		s.ExpireIn(p.maxLoginAge())
	}
	s.Set(p.sessionKey(), pending)
	return c.Redirect(http.StatusFound, p.Config.AuthCodeURL(state, opts...))
}

// Login is the result of a completed login.
type Login struct {
	Token *xoauth2.Token
	// User is the provider's answer at UserInfoURL (nil when UserInfoURL is
	// empty). Identify a user by the provider's stable id (GitHub: "id"), not
	// by e-mail address or login name, which can change hands.
	User map[string]any
}

// ErrLogin is the suffix of every Complete failure that means "this login
// attempt is not valid" (always a 401).
var ErrLogin = errors.New("login failed")

func fail(format string, a ...any) error {
	return aemeath.NewError(http.StatusUnauthorized, "aemeath/oauth2: "+fmt.Sprintf(format, a...)+": "+ErrLogin.Error())
}

// Complete finishes the login on the callback request: it checks the state
// (constant time) against the one Begin stored, exchanges the code (with the
// PKCE verifier when PKCE is on) and fetches UserInfoURL. A login can be
// completed once: the stored state is removed whatever happens. On success it
// renews the session id (see session.Session.Renew), so the app only sets
// who logged in.
func (p *Plugin) Complete(c *aemeath.Ctx) (*Login, error) {
	s, err := sessionOf(c)
	if err != nil {
		return nil, err
	}
	raw, _ := s.Get(p.sessionKey())
	s.Delete(p.sessionKey())
	m, _ := raw.(map[string]any)
	state, _ := m["s"].(string)
	verifier, _ := m["v"].(string)
	started, _ := m["t"].(float64)
	if t, ok := m["t"].(int64); ok {
		started = float64(t)
	}
	if state == "" {
		return nil, fail("no login in progress")
	}
	if time.Since(time.Unix(int64(started), 0)) > p.maxLoginAge() {
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
	hc := p.httpClient()
	limit := hc.Timeout
	if limit <= 0 {
		limit = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.WithValue(c.Context(), xoauth2.HTTPClient, hc), limit)
	defer cancel()
	var opts []xoauth2.AuthCodeOption
	if verifier != "" {
		opts = append(opts, xoauth2.VerifierOption(verifier))
	}
	tok, err := p.Config.Exchange(ctx, code, opts...)
	if err != nil {
		return nil, fail("code exchange failed")
	}
	login := &Login{Token: tok}
	if p.UserInfoURL != "" {
		if login.User, err = p.UserInfo(ctx, tok); err != nil {
			return nil, err
		}
	}
	// A successful login gets a new session id, so an id planted in the
	// browser before the login cannot share it (session fixation).
	if err := s.Renew(c.Context()); err != nil {
		return nil, err
	}
	return login, nil
}

// sanitize keeps a provider's error code printable and short.
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
