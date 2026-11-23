package jwt_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/jwt"
)

// slowSigner is a JWKS provider that answers after a delay, with the private
// key to sign tokens for it.
type slowSigner struct {
	srv   *httptest.Server
	key   *rsa.PrivateKey
	delay time.Duration
}

func newSlowSigner(t *testing.T, delay time.Duration) *slowSigner {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	body, _ := json.Marshal(map[string]any{"keys": []any{map[string]any{
		"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
		"n": enc(key.N.Bytes()), "e": enc(big.NewInt(int64(key.E)).Bytes()),
	}}})
	s := &slowSigner{key: key, delay: delay}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(s.delay):
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *slowSigner) token(t *testing.T) string {
	t.Helper()
	tok := gojwt.NewWithClaims(gojwt.SigningMethodRS256, gojwt.MapClaims{"sub": "u1", "exp": time.Now().Add(time.Hour).Unix()})
	tok.Header["kid"] = "k1"
	str, err := tok.SignedString(s.key)
	if err != nil {
		t.Fatal(err)
	}
	return str
}

// A client that leaves while the JWKS is being fetched must not keep its
// request waiting until the HTTP client's timeout: the lookup follows the
// request's context.
func TestMiddlewareStopsWaitingForTheJWKSWhenTheClientLeaves(t *testing.T) {
	p := newSlowSigner(t, 3*time.Second)
	app := aemeath.New()
	app.Use(jwt.Middleware(jwt.Config{JWKSURL: p.srv.URL}))
	app.Handle("GET", "/me", func(c *aemeath.Ctx) error { return c.JSON(200, map[string]string{"sub": jwt.UserID(c)}) })

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/me", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+p.token(t))
	start := time.Now()
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if took := time.Since(start); took > time.Second {
		t.Fatalf("the request waited %v for a JWKS fetch after its client had left (200 ms); want it to stop with the client", took.Round(time.Millisecond))
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// The fetch is shared: when the request that started it goes away, the other
// requests waiting for the same key must still get it.
func TestJWKSFetchSurvivesTheRequestThatStartedIt(t *testing.T) {
	p := newSlowSigner(t, 500*time.Millisecond)
	set, err := jwt.NewJWKS(p.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	leaving, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	first := make(chan error, 1)
	go func() { _, _, err := set.Key(leaving, "k1"); first <- err }()
	time.Sleep(20 * time.Millisecond) // the first caller starts the fetch

	key, _, err := set.Key(context.Background(), "k1")
	if err != nil || key == nil {
		t.Fatalf("a request waiting for the shared fetch got key=%v err=%v after the request that started it left; want the key", key, err)
	}
	if err := <-first; err == nil {
		t.Fatalf("the request whose context ended got no error")
	}

	v := jwt.NewVerifier(jwt.Config{JWKS: set})
	if _, err := v.VerifyContext(context.Background(), p.token(t)); err != nil {
		t.Fatalf("VerifyContext: %v", err)
	}
}
