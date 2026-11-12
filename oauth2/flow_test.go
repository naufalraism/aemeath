package oauth2_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	xoauth2 "golang.org/x/oauth2"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/oauth2"
)

// fakeProvider is an OAuth2 provider that checks what the client sends, the way a real one does.
func fakeProvider(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id, secret, hasBasic := r.BasicAuth()
		if !hasBasic {
			id, secret = r.Form.Get("client_id"), r.Form.Get("client_secret")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "good-code" ||
			id != "client-id" || secret != "client-secret" || r.Form.Get("redirect_uri") != "http://app.test/auth/callback" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"tok-123","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"email":"ana@example.com","name":"Ana","verified":true}`))
	})
	mux.HandleFunc("/broken", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
	mux.HandleFunc("/huge", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"blob":"` + strings.Repeat("a", 2<<20) + `"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newPlugin(provider *httptest.Server, userInfoPath string) *oauth2.Plugin {
	return oauth2.New("fake", &xoauth2.Config{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURL:  "http://app.test/auth/callback",
		Scopes:       []string{"openid", "email"},
		Endpoint:     xoauth2.Endpoint{AuthURL: provider.URL + "/auth", TokenURL: provider.URL + "/token"},
	}, provider.URL+userInfoPath)
}

func TestAuthURLCarriesTheStateAndTheClient(t *testing.T) {
	p := newPlugin(fakeProvider(t), "/userinfo")
	u, err := url.Parse(p.AuthURL("state-xyz"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Path != "/auth" || q.Get("state") != "state-xyz" || q.Get("client_id") != "client-id" ||
		q.Get("redirect_uri") != "http://app.test/auth/callback" || q.Get("response_type") != "code" || q.Get("scope") != "openid email" {
		t.Fatalf("auth URL = %s", u)
	}
}

func TestExchangeAndUserInfoAgainstTheProvider(t *testing.T) {
	provider := fakeProvider(t)
	p := newPlugin(provider, "/userinfo")
	ctx := t.Context()

	tok, err := p.Exchange(ctx, "good-code")
	if err != nil || tok.AccessToken != "tok-123" {
		t.Fatalf("Exchange = %+v, %v", tok, err)
	}
	info, err := p.UserInfo(ctx, tok)
	if err != nil || info["email"] != "ana@example.com" || info["verified"] != true {
		t.Fatalf("UserInfo = %v, %v", info, err)
	}

	if _, err := p.Exchange(ctx, "forged-code"); err == nil {
		t.Error("a code the provider does not know must fail")
	}
	if _, err := p.UserInfo(ctx, &xoauth2.Token{AccessToken: "stolen", TokenType: "Bearer"}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("a rejected token must report the status: %v", err)
	}
	if _, err := newPlugin(provider, "/broken").UserInfo(ctx, tok); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("a provider error must report the status: %v", err)
	}
	if _, err := newPlugin(provider, "/huge").UserInfo(ctx, tok); err == nil {
		t.Error("a response over 1 MiB must be refused instead of being read into memory")
	}
}

// The whole login, through aemeath handlers, with the state cookie that stops CSRF.
func TestLoginFlowChecksTheState(t *testing.T) {
	provider := fakeProvider(t)
	app := aemeath.New()
	if err := app.RegisterPlugin(newPlugin(provider, "/userinfo")); err != nil {
		t.Fatal(err)
	}

	aemeath.GET(app, "/auth/login", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
		p, _ := oauth2.From(c, "fake")
		state, err := oauth2.GenerateState()
		if err != nil {
			return nil, err
		}
		http.SetCookie(c.W, &http.Cookie{Name: "oauth_state", Value: state, Path: "/auth", HttpOnly: true, SameSite: http.SameSiteLaxMode})
		return map[string]string{"url": p.AuthURL(state)}, nil
	})
	aemeath.GET(app, "/auth/callback", func(c *aemeath.Ctx, in struct {
		Code  string `query:"code" validate:"required"`
		State string `query:"state" validate:"required"`
	}) (map[string]any, error) {
		cookie, err := c.R.Cookie("oauth_state")
		if err != nil || cookie.Value != in.State {
			return nil, aemeath.BadRequest("invalid state")
		}
		p, _ := oauth2.From(c, "fake")
		tok, err := p.Exchange(c.Context(), in.Code)
		if err != nil {
			return nil, aemeath.Unauthorized("failed to exchange the token")
		}
		info, err := p.UserInfo(c.Context(), tok)
		if err != nil {
			return nil, err
		}
		return info, nil
	})

	do := func(target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", target, nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}

	// 1. The user starts the login: a state cookie and a URL that carries the same state.
	rec := do("/auth/login")
	var login struct{ URL string }
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &login) != nil {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	cookie := rec.Result().Cookies()[0]
	u, _ := url.Parse(login.URL)
	state := u.Query().Get("state")
	if state == "" || cookie.Value != state || !cookie.HttpOnly {
		t.Fatalf("cookie %+v does not match the URL state %q", cookie, state)
	}

	// 2. The provider sends the user back with the code and the same state.
	rec = do("/auth/callback?code=good-code&state="+url.QueryEscape(state), cookie)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "ana@example.com") {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body)
	}

	// 3. Attacks: a different state, no cookie, a forged code.
	if rec = do("/auth/callback?code=good-code&state=attacker-state", cookie); rec.Code != 400 {
		t.Errorf("a state that does not match the cookie must be refused, got %d", rec.Code)
	}
	if rec = do("/auth/callback?code=good-code&state=" + url.QueryEscape(state)); rec.Code != 400 {
		t.Errorf("a callback without the state cookie must be refused (CSRF), got %d", rec.Code)
	}
	if rec = do("/auth/callback?code=forged&state="+url.QueryEscape(state), cookie); rec.Code != 401 {
		t.Errorf("a forged code must be refused, got %d", rec.Code)
	}
	if rec = do("/auth/callback?state=x", cookie); rec.Code != 422 {
		t.Errorf("a missing code is a validation error, got %d", rec.Code)
	}
}
