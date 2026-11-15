package securityaudit

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/jwt"
)

func jwtApp(cfg jwt.Config) *aemeath.App {
	app := aemeath.New()
	app.Use(jwt.Middleware(cfg))
	app.Handle("GET", "/me", func(c *aemeath.Ctx) error { return c.Text(200, "user="+jwt.UserID(c)) })
	return app
}

func withToken(app *aemeath.App, tok string) int {
	r := httptest.NewRequest("GET", "/me", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w.Code
}

func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func TestJWT_ForgeryAttempts(t *testing.T) {
	const secret = "correct-horse-battery-staple-0123456789"
	app := jwtApp(jwt.Config{SecretKey: secret, Issuer: "https://auth.example", Audience: "api"})
	future := time.Now().Add(time.Hour).Unix()
	sign := func(claims gojwt.MapClaims, key string) string {
		s, _ := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims).SignedString([]byte(key))
		return s
	}
	good := gojwt.MapClaims{"sub": "42", "exp": future, "iss": "https://auth.example", "aud": "api"}

	cases := map[string]struct {
		tok  string
		want int
	}{
		"valid token (control)": {sign(good, secret), 200},
		"alg=none":              {b64(`{"alg":"none","typ":"JWT"}`) + "." + b64(`{"sub":"admin","exp":9999999999,"iss":"https://auth.example","aud":"api"}`) + ".", 401},
		"wrong secret":          {sign(good, "guess"), 401},
		"no exp":                {sign(gojwt.MapClaims{"sub": "42", "iss": "https://auth.example", "aud": "api"}, secret), 401},
		"expired":               {sign(gojwt.MapClaims{"sub": "42", "exp": time.Now().Add(-time.Minute).Unix(), "iss": "https://auth.example", "aud": "api"}, secret), 401},
		"wrong issuer":          {sign(gojwt.MapClaims{"sub": "42", "exp": future, "iss": "https://evil", "aud": "api"}, secret), 401},
		"wrong audience":        {sign(gojwt.MapClaims{"sub": "42", "exp": future, "iss": "https://auth.example", "aud": "other"}, secret), 401},
		"nbf in the future":     {sign(gojwt.MapClaims{"sub": "42", "exp": future, "nbf": future, "iss": "https://auth.example", "aud": "api"}, secret), 401},
		"signature stripped":    {sign(good, secret)[:len(sign(good, secret))-43] + "AAAA", 401},
		"empty":                 {"", 401},
	}
	for name, tc := range cases {
		got := withToken(app, tc.tok)
		t.Logf("%-24s -> %d", name, got)
		if got != tc.want {
			t.Errorf("WEAKNESS: %s: got %d, want %d", name, got, tc.want)
		}
	}
}

// Algorithm confusion: with an RSA public key configured, an attacker signs
// with HS256 using the (public) PEM as the HMAC secret.
func TestJWT_AlgorithmConfusion(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	app := jwtApp(jwt.Config{PublicKey: &priv.PublicKey})

	claims := gojwt.MapClaims{"sub": "admin", "exp": time.Now().Add(time.Hour).Unix()}
	forged, _ := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims).SignedString(pemBytes)
	legit, _ := gojwt.NewWithClaims(gojwt.SigningMethodRS256, claims).SignedString(priv)
	t.Logf("RS256 legit -> %d, HS256 signed with the public PEM -> %d", withToken(app, legit), withToken(app, forged))
	if withToken(app, forged) != 401 {
		t.Error("WEAKNESS: HS256 token signed with the public key was accepted")
	}
}

// With a JWKS URL, a provider that is down or slow at startup must not turn
// every incoming request into a fetch that holds a global lock.
func TestJWT_JWKSProviderDownSerializesRequests(t *testing.T) {
	var fetches atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		time.Sleep(300 * time.Millisecond) // a slow, failing identity provider
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer provider.Close()
	set, err := jwt.NewJWKS(provider.URL) // http://127.0.0.1 is allowed (loopback)
	if err != nil {
		t.Fatal(err)
	}
	app := jwtApp(jwt.Config{JWKS: set})

	tok := b64(`{"alg":"RS256","kid":"k1"}`) + "." + b64(`{"sub":"x","exp":9999999999}`) + ".c2ln"
	const n = 10
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); withToken(app, tok) }()
	}
	wg.Wait()
	took := time.Since(start)
	t.Logf("%d concurrent requests with a bogus token: %d JWKS fetches, %v total", n, fetches.Load(), took.Round(10*time.Millisecond))
	if fetches.Load() > 1 {
		t.Errorf("WEAKNESS: %d fetches for %d requests (no rate limit while no key was ever loaded); requests waited %v in a row behind one lock", fetches.Load(), n, took.Round(10*time.Millisecond))
	}
}

// HS256 with a short secret can be brute-forced offline from any token the
// attacker holds (their own login token). Nothing rejects a weak SecretKey.
func TestJWT_WeakSecretAccepted(t *testing.T) {
	for _, s := range []string{"secret", "123456", "changeme"} {
		ok := func() (accepted bool) {
			defer func() {
				if recover() != nil {
					accepted = false
				}
			}()
			jwt.Middleware(jwt.Config{SecretKey: s})
			return true
		}()
		t.Logf("jwt.Config{SecretKey: %q} accepted at startup: %v", s, ok)
		if ok {
			t.Errorf("WEAKNESS: a %d-byte HMAC secret is accepted without error or warning (HS256 needs at least 32 random bytes)", len(s))
		}
	}
}
