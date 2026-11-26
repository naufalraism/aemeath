package oauth2_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	xoauth2 "golang.org/x/oauth2"

	"github.com/naufalraism/aemeath/oauth2"
)

type countingTransport struct{ n atomic.Int32 }

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return http.DefaultTransport.RoundTrip(r)
}

func provider(t *testing.T, h http.HandlerFunc, hc *http.Client) *oauth2.Plugin {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return oauth2.New(oauth2.Config{
		Name:        "gh",
		OAuth2:      &xoauth2.Config{ClientID: "id", Endpoint: xoauth2.Endpoint{AuthURL: srv.URL + "/authorize", TokenURL: srv.URL + "/token"}},
		UserInfoURL: srv.URL + "/user",
		HTTPClient:  hc,
	})
}

// Config.HTTPClient is documented as the client for the code exchange and
// the user info request; called directly, Exchange and UserInfo used
// http.DefaultClient instead.
func TestExchangeAndUserInfoUseTheConfiguredClient(t *testing.T) {
	tr := &countingTransport{}
	p := provider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/token" {
			_, _ = w.Write([]byte(`{"access_token":"a","token_type":"bearer"}`))
			return
		}
		_, _ = w.Write([]byte(`{"login":"ann"}`))
	}, &http.Client{Transport: tr})

	tok, err := p.Exchange(context.Background(), "code")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UserInfo(context.Background(), tok); err != nil {
		t.Fatal(err)
	}
	if got := tr.n.Load(); got != 2 {
		t.Fatalf("the configured client sent %d request(s), want 2 (exchange and user info)", got)
	}
}

// http.DefaultClient has no timeout: a provider that never answers held the
// caller for ever.
func TestExchangeAndUserInfoStopAtTheClientTimeout(t *testing.T) {
	release := make(chan struct{})
	p := provider(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}, &http.Client{Timeout: 200 * time.Millisecond})
	t.Cleanup(func() { close(release) }) // runs before the server's Close, which waits for handlers

	for name, call := range map[string]func() error{
		"Exchange": func() error { _, err := p.Exchange(context.Background(), "code"); return err },
		"UserInfo": func() error {
			_, err := p.UserInfo(context.Background(), &xoauth2.Token{AccessToken: "a"})
			return err
		},
	} {
		done := make(chan error, 1)
		go func() { done <- call() }()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("%s: no error from a provider that never answers", name)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("%s with a 200ms client timeout is still waiting after 2s", name)
		}
	}
}
