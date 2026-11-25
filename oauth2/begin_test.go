package oauth2_test

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	xoauth2 "golang.org/x/oauth2"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/oauth2"
	"github.com/naufalraism/aemeath/session"
)

// pkceProvider is a fake provider that remembers the code challenge sent to
// /auth and, at /token, accepts the code only with the matching verifier
// when a challenge was sent.
type pkceProvider struct {
	*httptest.Server
	mu        sync.Mutex
	challenge string
	verifiers []string
}

func newPKCEProvider(t *testing.T) *pkceProvider {
	t.Helper()
	p := &pkceProvider{}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.challenge = r.URL.Query().Get("code_challenge")
		p.mu.Unlock()
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		p.mu.Lock()
		challenge := p.challenge
		v := r.Form.Get("code_verifier")
		p.verifiers = append(p.verifiers, v)
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		ok := r.Form.Get("code") == "good-code"
		if challenge != "" {
			sum := sha256.Sum256([]byte(v))
			ok = ok && base64.RawURLEncoding.EncodeToString(sum[:]) == challenge
		}
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"tok-123","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"id":42,"login":"ana"}`))
	})
	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)
	return p
}

func (p *pkceProvider) plugin(name string) *oauth2.Plugin {
	return oauth2.New(name, &xoauth2.Config{
		ClientID: "client-id", ClientSecret: "client-secret",
		RedirectURL: "http://app.test/callback",
		Endpoint:    xoauth2.Endpoint{AuthURL: p.URL + "/auth", TokenURL: p.URL + "/token"},
	}, p.URL+"/user")
}

// loginApp serves /login/{name} (Begin) and /callback/{name} (Complete) for
// each plugin, behind session.Middleware.
func loginApp(t *testing.T, plugins ...*oauth2.Plugin) (*httptest.Server, *http.Client) {
	t.Helper()
	app := aemeath.New()
	app.Use(session.Middleware(session.Config{Store: session.NewMemoryStore(), Insecure: true}))
	for _, p := range plugins {
		p := p
		app.Handle("GET", "/login/"+p.ProviderName, func(c *aemeath.Ctx) error { return p.Begin(c) })
		app.Handle("GET", "/callback/"+p.ProviderName, func(c *aemeath.Ctx) error {
			login, err := p.Complete(c)
			if err != nil {
				return err
			}
			return c.JSON(200, login.User)
		})
	}
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return srv, client
}

// begin calls the login route and returns the provider URL it redirects to.
func begin(t *testing.T, srv *httptest.Server, client *http.Client, name string) *url.URL {
	t.Helper()
	resp, err := client.Get(srv.URL + "/login/" + name)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("Begin: status %d, want 302", resp.StatusCode)
	}
	u, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func callback(t *testing.T, srv *httptest.Server, client *http.Client, name, query string) (int, string) {
	t.Helper()
	resp, err := client.Get(srv.URL + "/callback/" + name + "?" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestBeginAndCompleteLogIn(t *testing.T) {
	prov := newPKCEProvider(t)
	gh := prov.plugin("gh")
	srv, client := loginApp(t, gh)

	u := begin(t, srv, client, "gh")
	state := u.Query().Get("state")
	if state == "" || u.Query().Get("client_id") != "client-id" || u.Query().Has("code_challenge") {
		t.Fatalf("redirect = %s; want a state, the client id and no PKCE (off by default)", u)
	}
	code, body := callback(t, srv, client, "gh", "state="+url.QueryEscape(state)+"&code=good-code")
	if code != 200 || !strings.Contains(body, `"login":"ana"`) {
		t.Fatalf("Complete: %d %s", code, body)
	}

	// The same callback again is refused: a login completes once.
	if code, body := callback(t, srv, client, "gh", "state="+url.QueryEscape(state)+"&code=good-code"); code != 401 || !strings.Contains(body, "no login in progress") {
		t.Errorf("replayed callback: %d %s, want 401 no login in progress", code, body)
	}
}

func TestCompleteRefusesABadCallback(t *testing.T) {
	prov := newPKCEProvider(t)
	gh := prov.plugin("gh")
	srv, client := loginApp(t, gh)

	for _, tc := range []struct {
		name, query, want string // query: %s is replaced by the real state
	}{
		{"no login started", "state=x&code=good-code", "no login in progress"},
		{"wrong state", "state=forged&code=good-code", "state mismatch"},
		{"no state", "code=good-code", "state mismatch"},
		{"provider error", "state=%s&error=access_denied%0A", "the provider returned an error (access_denied?)"},
		{"no code", "state=%s", "missing code"},
		{"bad code", "state=%s&code=stolen", "code exchange failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := tc.query
			if strings.Contains(q, "%s") {
				state := begin(t, srv, client, "gh").Query().Get("state")
				q = strings.Replace(q, "%s", url.QueryEscape(state), 1)
			} else if tc.name != "no login started" {
				begin(t, srv, client, "gh")
			}
			code, body := callback(t, srv, client, "gh", q)
			if code != 401 || !strings.Contains(body, tc.want) {
				t.Errorf("%d %s, want 401 containing %q", code, body, tc.want)
			}
		})
	}

	// A failed attempt uses up the login: the right state afterwards fails too.
	state := begin(t, srv, client, "gh").Query().Get("state")
	callback(t, srv, client, "gh", "state=forged&code=good-code")
	if code, _ := callback(t, srv, client, "gh", "state="+url.QueryEscape(state)+"&code=good-code"); code != 401 {
		t.Errorf("the right state after a failed attempt: %d, want 401", code)
	}
}

// The state of one provider is not accepted by another.
func TestEachProviderHasItsOwnState(t *testing.T) {
	prov := newPKCEProvider(t)
	a, b := prov.plugin("a"), prov.plugin("b")
	srv, client := loginApp(t, a, b)
	stateA := begin(t, srv, client, "a").Query().Get("state")
	begin(t, srv, client, "b")
	if code, _ := callback(t, srv, client, "b", "state="+url.QueryEscape(stateA)+"&code=good-code"); code != 401 {
		t.Errorf("provider b accepted provider a's state: %d", code)
	}
	if code, _ := callback(t, srv, client, "a", "state="+url.QueryEscape(stateA)+"&code=good-code"); code != 200 {
		t.Errorf("provider a's own login: %d, want 200", code)
	}
}

func TestLoginTakingTooLongIsRefused(t *testing.T) {
	prov := newPKCEProvider(t)
	gh := prov.plugin("gh")
	gh.MaxLoginAge = time.Nanosecond
	// A visitor without a session gets one that expires with the login, so
	// start the session through another provider first: the login, not the
	// session, must be what is too old.
	srv, client := loginApp(t, prov.plugin("warm"), gh)
	begin(t, srv, client, "warm")
	state := begin(t, srv, client, "gh").Query().Get("state")
	if code, body := callback(t, srv, client, "gh", "state="+url.QueryEscape(state)+"&code=good-code"); code != 401 || !strings.Contains(body, "took too long") {
		t.Errorf("%d %s, want 401 took too long", code, body)
	}
}

func TestPKCEWhenTurnedOn(t *testing.T) {
	prov := newPKCEProvider(t)
	gh := prov.plugin("gh")
	gh.PKCE = true
	srv, client := loginApp(t, gh)
	u := begin(t, srv, client, "gh")
	if u.Query().Get("code_challenge") == "" || u.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("redirect without an S256 code challenge: %s", u)
	}
	// The provider sees the challenge, as a browser following the redirect would.
	resp, err := http.Get(u.String())
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	code, body := callback(t, srv, client, "gh", "state="+url.QueryEscape(u.Query().Get("state"))+"&code=good-code")
	if code != 200 {
		t.Fatalf("Complete with PKCE: %d %s", code, body)
	}
	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.verifiers) != 1 || prov.verifiers[0] == "" {
		t.Errorf("verifiers sent to /token: %q", prov.verifiers)
	}
}

func TestBeginWithoutSessionMiddlewareSaysSo(t *testing.T) {
	gh := newPKCEProvider(t).plugin("gh")
	app := aemeath.New()
	var beginErr, completeErr error
	app.Handle("GET", "/login", func(c *aemeath.Ctx) error { beginErr = gh.Begin(c); return nil })
	app.Handle("GET", "/callback", func(c *aemeath.Ctx) error { _, completeErr = gh.Complete(c); return nil })
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/login", nil))
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/callback?state=x&code=y", nil))
	for name, err := range map[string]error{"Begin": beginErr, "Complete": completeErr} {
		if err == nil || !strings.Contains(err.Error(), "need session.Middleware") {
			t.Errorf("%s without session.Middleware: %v", name, err)
		}
	}
}

// A successful Complete is a login, so it renews the session id itself (see
// the oidc test of the same name).
func TestCompleteRenewsTheSessionID(t *testing.T) {
	prov := newPKCEProvider(t)
	gh := prov.plugin("gh")
	srv, client := loginApp(t, gh)
	u := begin(t, srv, client, "gh")
	base, _ := url.Parse(srv.URL)
	idOf := func() string {
		for _, c := range client.Jar.Cookies(base) {
			if c.Name == "aemeath_session" {
				return c.Value
			}
		}
		return ""
	}
	before := idOf()
	if code, body := callback(t, srv, client, "gh", "state="+url.QueryEscape(u.Query().Get("state"))+"&code=good-code"); code != 200 {
		t.Fatalf("Complete: %d %s", code, body)
	}
	if after := idOf(); after == "" || after == before {
		t.Fatalf("the session id was not renewed: before %q, after %q", before, after)
	}
	// The old id no longer finds a session: a login started with it is gone.
	old := &http.Client{CheckRedirect: client.CheckRedirect}
	req, _ := http.NewRequest("GET", srv.URL+"/callback/gh?state=x&code=good-code", nil)
	req.AddCookie(&http.Cookie{Name: "aemeath_session", Value: before})
	resp, err := old.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 401 || !strings.Contains(string(b), "no login in progress") {
		t.Errorf("the pre-login id: %d %s", resp.StatusCode, b)
	}
}
