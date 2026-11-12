package oidc_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/oidc"
	"github.com/naufalraism/aemeath/session"
)

const clientID = "client-1"

// fakeIdP is a minimal OpenID provider: discovery, JWKS and a token endpoint
// that checks the PKCE verifier like a real one.
type fakeIdP struct {
	srv       *httptest.Server
	key       *rsa.PrivateKey
	mu        sync.Mutex
	challenge string
	nonce     string
	issuerDoc string // what discovery claims as issuer (tests can lie)
	tamper    func(claims map[string]any)
	signWith  *rsa.PrivateKey
	pkceSeen  bool
}

func newIdP(t *testing.T) *fakeIdP {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	f := &fakeIdP{key: k, signWith: k}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		iss := f.srv.URL
		if f.issuerDoc != "" {
			iss = f.issuerDoc
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": iss, "authorization_endpoint": f.srv.URL + "/auth", "token_endpoint": f.srv.URL + "/token",
			"jwks_uri": f.srv.URL + "/jwks", "code_challenge_methods_supported": []string{"S256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		ver := r.PostForm.Get("code_verifier")
		sum := sha256S256(ver)
		if ver == "" || sum != f.challenge {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		f.pkceSeen = true
		claims := gojwt.MapClaims{"nonce": f.nonce, "email": "ann@example.com", "email_verified": true, "name": "Ann",
			"sub": "user-123", "iss": f.srv.URL, "aud": clientID, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix()}
		if f.tamper != nil {
			f.tamper(claims)
		}
		jt := gojwt.NewWithClaims(gojwt.SigningMethodRS256, claims)
		jt.Header["kid"] = "k1"
		tok, _ := jt.SignedString(f.signWith)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": tok})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) app(t *testing.T) (*aemeath.App, *oidc.Provider) {
	p, err := oidc.Discover(context.Background(), oidc.Config{Issuer: f.srv.URL, ClientID: clientID, ClientSecret: "s", RedirectURL: "http://localhost/cb"})
	if err != nil {
		t.Fatal(err)
	}
	app := aemeath.New()
	app.Use(session.Middleware(session.Config{Store: session.NewMemoryStore(), Insecure: true}))
	app.Handle("GET", "/login", func(c *aemeath.Ctx) error { return p.Begin(c) })
	app.Handle("GET", "/cb", func(c *aemeath.Ctx) error {
		id, err := p.Complete(c)
		if err != nil {
			return err
		}
		return c.JSON(200, map[string]any{"iss": id.Issuer, "sub": id.Subject, "email": id.Email, "verified": id.EmailVerified})
	})
	return app, p
}

// login drives Begin, "visits" the provider, and returns the callback request parts.
func (f *fakeIdP) login(t *testing.T, app *aemeath.App) (cookie *http.Cookie, state string) {
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/login", nil))
	if w.Code != 302 {
		t.Fatalf("begin: %d %s", w.Code, w.Body)
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	q := loc.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") == "" || q.Get("state") == "" || q.Get("client_id") != clientID {
		t.Fatalf("authorization URL lacks PKCE/nonce/state: %s", loc)
	}
	f.mu.Lock()
	f.challenge, f.nonce = q.Get("code_challenge"), q.Get("nonce")
	f.mu.Unlock()
	for _, c := range w.Result().Cookies() {
		if c.Name == "aemeath_session" {
			cookie = c
		}
	}
	return cookie, q.Get("state")
}

func callback(app *aemeath.App, c *http.Cookie, query string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/cb?"+query, nil)
	if c != nil {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}

func TestFullLoginFlow(t *testing.T) {
	f := newIdP(t)
	app, _ := f.app(t)
	ck, state := f.login(t, app)
	w := callback(app, ck, "code=abc&state="+state)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	b := w.Body.String()
	if !strings.Contains(b, `"sub":"user-123"`) || !strings.Contains(b, `"iss":"`+f.srv.URL+`"`) || !strings.Contains(b, `"verified":true`) {
		t.Fatalf("identity wrong: %s", b)
	}
	if !f.pkceSeen {
		t.Fatal("the PKCE verifier never reached the token endpoint")
	}
}

func TestStateMismatchMissingAndReplayAreRejected(t *testing.T) {
	f := newIdP(t)
	app, _ := f.app(t)
	ck, state := f.login(t, app)
	if w := callback(app, ck, "code=abc&state=forged"); w.Code != 401 {
		t.Fatalf("forged state: %d", w.Code)
	}
	// the attempt is consumed even by a failure: the right state can no longer be used
	if w := callback(app, ck, "code=abc&state="+state); w.Code != 401 {
		t.Fatalf("login attempt must be single use: %d", w.Code)
	}
	if w := callback(app, nil, "code=abc&state=x"); w.Code != 401 {
		t.Fatalf("callback without a session: %d", w.Code)
	}
	ck, state = f.login(t, app)
	if w := callback(app, ck, "state="+state); w.Code != 401 {
		t.Fatalf("missing code: %d", w.Code)
	}
	ck, _ = f.login(t, app)
	if w := callback(app, ck, "error=access_denied"); w.Code != 401 {
		t.Fatalf("provider error: %d", w.Code)
	}
}

func TestIDTokenAttacksAreRejected(t *testing.T) {
	cases := map[string]func(f *fakeIdP){
		"wrong nonce":      func(f *fakeIdP) { f.tamper = func(c map[string]any) { c["nonce"] = "other" } },
		"no nonce":         func(f *fakeIdP) { f.tamper = func(c map[string]any) { delete(c, "nonce") } },
		"expired":          func(f *fakeIdP) { f.tamper = func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() } },
		"wrong audience":   func(f *fakeIdP) { f.tamper = func(c map[string]any) { c["aud"] = "someone-else" } },
		"wrong issuer":     func(f *fakeIdP) { f.tamper = func(c map[string]any) { c["iss"] = "https://evil.example" } },
		"foreign signer":   func(f *fakeIdP) { k, _ := rsa.GenerateKey(rand.Reader, 2048); f.signWith = k },
		"multi-aud no azp": func(f *fakeIdP) { f.tamper = func(c map[string]any) { c["aud"] = []string{clientID, "other"} } },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newIdP(t)
			setup(f)
			app, _ := f.app(t)
			ck, state := f.login(t, app)
			if w := callback(app, ck, "code=abc&state="+state); w.Code != 401 {
				t.Fatalf("accepted a bad id_token: %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestDiscoveryFailsClosed(t *testing.T) {
	f := newIdP(t)
	f.issuerDoc = "https://other.example" // mix-up: the document names another issuer
	_, err := oidc.Discover(context.Background(), oidc.Config{Issuer: f.srv.URL, ClientID: clientID, RedirectURL: "http://localhost/cb"})
	if err == nil || !strings.Contains(err.Error(), "issuer") {
		t.Fatalf("issuer mismatch accepted: %v", err)
	}
	for name, cfg := range map[string]oidc.Config{
		"plain http issuer":   {Issuer: "http://idp.example.com", ClientID: "c", RedirectURL: "https://a/cb"},
		"plain http redirect": {Issuer: f.srv.URL, ClientID: "c", RedirectURL: "http://app.example.com/cb"},
		"no client id":        {Issuer: f.srv.URL, RedirectURL: "https://a/cb"},
	} {
		if _, err := oidc.Discover(context.Background(), cfg); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestLoginExpires(t *testing.T) {
	f := newIdP(t)
	p, err := oidc.Discover(context.Background(), oidc.Config{Issuer: f.srv.URL, ClientID: clientID, RedirectURL: "http://localhost/cb", MaxLoginAge: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	app := aemeath.New()
	app.Use(session.Middleware(session.Config{Store: session.NewMemoryStore(), Insecure: true}))
	app.Handle("GET", "/login", func(c *aemeath.Ctx) error { return p.Begin(c) })
	app.Handle("GET", "/cb", func(c *aemeath.Ctx) error { _, err := p.Complete(c); return err })
	ck, state := f.login(t, app)
	time.Sleep(5 * time.Millisecond)
	if w := callback(app, ck, "code=abc&state="+state); w.Code != 401 {
		t.Fatalf("stale login accepted: %d", w.Code)
	}
}

func sha256S256(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
