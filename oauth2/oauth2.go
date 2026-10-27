// Package oauth2 handles login through OAuth2 providers (Google, GitHub, ...).
package oauth2

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	xoauth2 "golang.org/x/oauth2"

	"github.com/naufalraism/aemeath"
)

// Plugin is one OAuth2 provider.
type Plugin struct {
	ProviderName string
	Config       *xoauth2.Config
	UserInfoURL  string
}

// New creates a Plugin for a provider.
func New(providerName string, config *xoauth2.Config, userInfoURL string) *Plugin {
	return &Plugin{ProviderName: providerName, Config: config, UserInfoURL: userInfoURL}
}

// Name implements aemeath.Plugin.
func (p *Plugin) Name() string { return "oauth2_" + p.ProviderName }

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
func GenerateState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("aemeath/oauth2: failed to generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// AuthURL returns the URL that sends the user to the provider's login page.
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
