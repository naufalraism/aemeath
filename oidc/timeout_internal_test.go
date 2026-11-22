package oidc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/session"
)

// The token exchange must not wait forever on a slow provider (B6 of the
// security audit: the callback waited 30 s on a token endpoint that took
// 15 s, because the exchange used http.DefaultClient, which has no timeout,
// while discovery used a client with one).
func TestTokenExchangeHasATimeout(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer": srv.URL, "authorization_endpoint": srv.URL + "/auth",
				"token_endpoint": srv.URL + "/token", "jwks_uri": srv.URL + "/jwks",
			})
		case "/jwks":
			_, _ = w.Write([]byte(`{"keys":[]}`))
		}
	}))
	defer srv.Close()

	p, err := Discover(context.Background(), Config{Issuer: srv.URL, ClientID: "c", RedirectURL: "http://127.0.0.1/cb"})
	if err != nil {
		t.Fatal(err)
	}
	if got := p.httpClient(); got == http.DefaultClient || got.Timeout != 10*time.Second {
		t.Errorf("token exchange client: default=%v timeout=%v, want a client with a 10s timeout", got == http.DefaultClient, got.Timeout)
	}

	mine := &http.Client{Timeout: 3 * time.Second}
	p, err = Discover(context.Background(), Config{Issuer: srv.URL, ClientID: "c", RedirectURL: "http://127.0.0.1/cb", HTTPClient: mine})
	if err != nil {
		t.Fatal(err)
	}
	if p.httpClient() != mine {
		t.Error("token exchange does not use Config.HTTPClient")
	}
}

// The whole exchange has one time limit, even when x/oauth2 tries a second
// request: with a 300 ms client and a token endpoint that never answers, the
// callback must fail in well under a second (it took two timeouts before).
func TestTokenExchangeHasOneTimeLimit(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer": srv.URL, "authorization_endpoint": srv.URL + "/auth",
				"token_endpoint": srv.URL + "/token", "jwks_uri": srv.URL + "/jwks",
			})
		case "/jwks":
			_, _ = w.Write([]byte(`{"keys":[]}`))
		case "/token":
			select { // far longer than the client allows
			case <-r.Context().Done():
			case <-time.After(3 * time.Second):
			}
		}
	}))
	defer srv.Close()
	defer srv.CloseClientConnections()
	p, err := Discover(context.Background(), Config{Issuer: srv.URL, ClientID: "c", ClientSecret: "s", RedirectURL: "http://127.0.0.1/cb",
		HTTPClient: &http.Client{Timeout: 300 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	app := aemeath.New()
	app.Use(session.Middleware(session.Config{Store: session.NewMemoryStore(), Insecure: true}))
	app.Handle("GET", "/login", func(c *aemeath.Ctx) error { return p.Begin(c) })
	app.Handle("GET", "/cb", func(c *aemeath.Ctx) error {
		_, err := p.Complete(c)
		return err
	})
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/login", nil))
	loc, err := w.Result().Location()
	if err != nil || len(w.Result().Cookies()) == 0 {
		t.Fatalf("login: %d %v", w.Code, err)
	}
	r := httptest.NewRequest("GET", "/cb?code=x&state="+loc.Query().Get("state"), nil)
	r.AddCookie(w.Result().Cookies()[0])
	start := time.Now()
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, r)
	took := time.Since(start)
	t.Logf("callback: %d after %v", rec.Code, took.Round(10*time.Millisecond))
	if took > 500*time.Millisecond {
		t.Errorf("the exchange took %v with a 300ms client: more than one time limit", took)
	}
}
