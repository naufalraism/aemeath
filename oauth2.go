package aemeath

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
)

// OAuthPlugin adalah plugin untuk menangani login via OAuth2 (Google, GitHub, dll).
type OAuthPlugin struct {
	ProviderName string
	Config       *oauth2.Config
	UserInfoURL  string
}

// NewOAuth2 membuat instance OAuthPlugin baru.
func NewOAuth2(providerName string, config *oauth2.Config, userInfoURL string) *OAuthPlugin {
	return &OAuthPlugin{
		ProviderName: providerName,
		Config:       config,
		UserInfoURL:  userInfoURL,
	}
}

// Name memenuhi antarmuka Plugin.
func (o *OAuthPlugin) Name() string { return "oauth2_" + o.ProviderName }

// Apply menyuntikkan OAuthPlugin ke dalam Context.
func (o *OAuthPlugin) Apply(app *App) error {
	app.Use(func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) error {
			c.State["oauth"] = o
			return next(c)
		}
	})
	return nil
}

// GetAuthURL mengembalikan URL untuk me-redirect pengguna ke halaman login provider.
func (o *OAuthPlugin) GetAuthURL(state string) string {
	return o.Config.AuthCodeURL(state)
}

// Exchange menukar authorization code (dari callback) dengan Access Token.
func (o *OAuthPlugin) Exchange(ctx context.Context, code string) (*oauth2.Token, error) {
	return o.Config.Exchange(ctx, code)
}

// GetUserInfo mengambil data user dari provider menggunakan Access Token.
func (o *OAuthPlugin) GetUserInfo(ctx context.Context, token *oauth2.Token) (map[string]any, error) {
	client := o.Config.Client(ctx, token)
	resp, err := client.Get(o.UserInfoURL)
	if err != nil {
		return nil, fmt.Errorf("aemeath: gagal request user info: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("aemeath: provider mengembalikan status %d", resp.StatusCode)
	}

	var userInfo map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
		return nil, fmt.Errorf("aemeath: gagal decode user info: %w", err)
	}

	return userInfo, nil
}
