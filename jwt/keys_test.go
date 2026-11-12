package jwt_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"

	"github.com/naufalraism/aemeath/jwt"
)

var (
	keysOnce sync.Once
	rsaKey   *rsa.PrivateKey
	rsaKey2  *rsa.PrivateKey
	weakRSA  *rsa.PrivateKey
	ecKey    *ecdsa.PrivateKey
	edPub    ed25519.PublicKey
	edKey    ed25519.PrivateKey
)

func testKeys(t *testing.T) {
	t.Helper()
	keysOnce.Do(func() {
		var err error
		if rsaKey, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
			t.Fatal(err)
		}
		if rsaKey2, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
			t.Fatal(err)
		}
		if weakRSA, err = rsa.GenerateKey(rand.Reader, 1024); err != nil {
			t.Fatal(err)
		}
		if ecKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
			t.Fatal(err)
		}
		if edPub, edKey, err = ed25519.GenerateKey(rand.Reader); err != nil {
			t.Fatal(err)
		}
	})
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func rsaJWK(kid string, pub *rsa.PublicKey, extra map[string]any) map[string]any {
	m := map[string]any{"kty": "RSA", "kid": kid, "n": b64(pub.N.Bytes()), "e": b64(big.NewInt(int64(pub.E)).Bytes())}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func ecJWK(kid string, pub *ecdsa.PublicKey) map[string]any {
	point, _ := pub.Bytes() // 0x04 || X || Y
	half := (len(point) - 1) / 2
	return map[string]any{"kty": "EC", "crv": "P-256", "kid": kid, "x": b64(point[1 : 1+half]), "y": b64(point[1+half:])}
}

func edJWK(kid string, pub ed25519.PublicKey) map[string]any {
	return map[string]any{"kty": "OKP", "crv": "Ed25519", "kid": kid, "x": b64(pub)}
}

// provider is a JWKS endpoint whose keys and availability tests can change.
type provider struct {
	srv    *httptest.Server
	mu     sync.Mutex
	keys   []map[string]any
	status int
	hits   atomic.Int32
}

func newProvider(t *testing.T, keys ...map[string]any) *provider {
	t.Helper()
	p := &provider{keys: keys, status: 200}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.hits.Add(1)
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.status != 200 {
			w.WriteHeader(p.status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": p.keys})
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *provider) set(status int, keys ...map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status, p.keys = status, keys
}

func issue(t *testing.T, alg string, key any, kid string) string {
	t.Helper()
	tok, err := jwt.Issue(alg, key, jwt.IssueOptions{Subject: "user-1", TTL: time.Hour, KeyID: kid})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func status(app http.Handler, token string) int {
	return call(app, "Bearer "+token).Code
}

func TestPublicKeysVerifyTheirOwnSignaturesOnly(t *testing.T) {
	testKeys(t)
	otherEC, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	_, otherEd, _ := ed25519.GenerateKey(rand.Reader)
	cases := []struct {
		name     string
		alg      string
		priv     any
		pub      crypto.PublicKey
		otherPrv any
	}{
		{"RS256", "RS256", rsaKey, &rsaKey.PublicKey, rsaKey2},
		{"PS256", "PS256", rsaKey, &rsaKey.PublicKey, rsaKey2},
		{"ES256", "ES256", ecKey, &ecKey.PublicKey, otherEC},
		{"EdDSA", "EdDSA", edKey, edPub, otherEd},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := build(jwt.Config{PublicKey: tc.pub})
			if got := status(app, issue(t, tc.alg, tc.priv, "")); got != 200 {
				t.Fatalf("a token signed with the matching private key: %d, want 200", got)
			}
			if got := status(app, issue(t, tc.alg, tc.otherPrv, "")); got != 401 {
				t.Fatalf("a token signed with another key: %d, want 401", got)
			}
		})
	}
}

// The classic attack: the verifier holds a public key, the attacker "signs" a
// token with HS256 using that public key as the HMAC secret.
func TestAlgorithmConfusionIsRefused(t *testing.T) {
	testKeys(t)
	der, _ := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	claims := gojwt.MapClaims{"sub": "attacker", "exp": time.Now().Add(time.Hour).Unix()}

	app := build(jwt.Config{PublicKey: &rsaKey.PublicKey})
	for name, secret := range map[string][]byte{"DER": der, "PEM": pemBytes, "PEM with newline trimmed": []byte(strings.TrimSpace(string(pemBytes)))} {
		forged, _ := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims).SignedString(secret)
		if got := status(app, forged); got != 401 {
			t.Errorf("HS256 token keyed with the public key (%s): %d, want 401", name, got)
		}
	}

	none, _ := gojwt.NewWithClaims(gojwt.SigningMethodNone, claims).SignedString(gojwt.UnsafeAllowNoneSignatureType)
	if got := status(app, none); got != 401 {
		t.Errorf("alg=none: %d, want 401", got)
	}
	if got := status(app, issue(t, "ES256", ecKey, "")); got != 401 {
		t.Errorf("an ES256 token against an RSA key: %d, want 401", got)
	}
	if got := status(build(jwt.Config{PublicKey: &ecKey.PublicKey}), issue(t, "RS256", rsaKey, "")); got != 401 {
		t.Errorf("an RS256 token against an EC key: %d, want 401", got)
	}
	// And through a JWKS, where the key type is not known in advance.
	p := newProvider(t, rsaJWK("k1", &rsaKey.PublicKey, nil))
	viaJWKS := build(jwt.Config{JWKSURL: p.srv.URL})
	forged, _ := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims).SignedString(der)
	if got := status(viaJWKS, forged); got != 401 {
		t.Errorf("HS256 token against a JWKS: %d, want 401", got)
	}
}

func TestJWKSVerifiesAndCaches(t *testing.T) {
	testKeys(t)
	p := newProvider(t, rsaJWK("k1", &rsaKey.PublicKey, nil))
	app := build(jwt.Config{JWKSURL: p.srv.URL})

	tok1 := issue(t, "RS256", rsaKey, "k1")
	for i := 0; i < 5; i++ {
		if got := status(app, tok1); got != 200 {
			t.Fatalf("request %d: %d", i, got)
		}
	}
	if p.hits.Load() != 1 {
		t.Fatalf("the provider was contacted %d times for 5 requests, want 1 (cached)", p.hits.Load())
	}
	if got := status(app, issue(t, "RS256", rsaKey2, "k2")); got != 401 {
		t.Fatalf("an unknown kid: %d, want 401", got)
	}
}

func TestJWKSRefreshesAfterAKeyRotation(t *testing.T) {
	testKeys(t)
	p := newProvider(t, rsaJWK("k1", &rsaKey.PublicKey, nil))
	set, err := jwt.NewJWKS(p.srv.URL, jwt.WithJWKSMinRefresh(0)) // refresh as soon as an unknown kid appears
	if err != nil {
		t.Fatal(err)
	}
	app := build(jwt.Config{JWKS: set})

	if got := status(app, issue(t, "RS256", rsaKey, "k1")); got != 200 {
		t.Fatalf("before the rotation: %d", got)
	}
	p.set(200, rsaJWK("k2", &rsaKey2.PublicKey, nil)) // the provider rotates: k1 is retired
	if got := status(app, issue(t, "RS256", rsaKey2, "k2")); got != 200 {
		t.Fatalf("a token from the new key after the rotation: %d, want 200", got)
	}
	if got := status(app, issue(t, "RS256", rsaKey, "k1")); got != 401 {
		t.Fatalf("a token from the retired key: %d, want 401", got)
	}
}

func TestBogusKidsCannotMakeTheServerHammerTheProvider(t *testing.T) {
	testKeys(t)
	p := newProvider(t, rsaJWK("k1", &rsaKey.PublicKey, nil))
	set, _ := jwt.NewJWKS(p.srv.URL, jwt.WithJWKSMinRefresh(time.Hour))
	app := build(jwt.Config{JWKS: set})

	status(app, issue(t, "RS256", rsaKey, "k1")) // warms the cache
	for i := 0; i < 200; i++ {
		status(app, issue(t, "ES256", ecKey, "random-"+strings.Repeat("x", i%7)+string(rune('a'+i%26))))
	}
	if hits := p.hits.Load(); hits > 2 {
		t.Fatalf("200 tokens with unknown kids caused %d requests to the provider (want at most 2)", hits)
	}
}

func TestJWKSKeepsWorkingWhenTheProviderGoesDown(t *testing.T) {
	testKeys(t)
	p := newProvider(t, rsaJWK("k1", &rsaKey.PublicKey, nil))
	set, _ := jwt.NewJWKS(p.srv.URL, jwt.WithJWKSTTL(time.Millisecond), jwt.WithJWKSMinRefresh(0))
	app := build(jwt.Config{JWKS: set})
	tok := issue(t, "RS256", rsaKey, "k1")

	if got := status(app, tok); got != 200 {
		t.Fatalf("while the provider is up: %d", got)
	}
	time.Sleep(5 * time.Millisecond) // the cache is now stale
	p.set(500)
	if got := status(app, tok); got != 200 {
		t.Fatalf("a stale cache must keep working during an outage: %d", got)
	}
}

func TestJWKSProviderDownFromTheStartMeansUnauthorizedNotACrash(t *testing.T) {
	testKeys(t)
	p := newProvider(t)
	p.set(503)
	app := build(jwt.Config{JWKSURL: p.srv.URL})
	if got := status(app, issue(t, "RS256", rsaKey, "k1")); got != 401 {
		t.Fatalf("%d, want 401", got)
	}
	p.srv.Close()
	if got := status(app, issue(t, "RS256", rsaKey, "k1")); got != 401 {
		t.Fatalf("with the provider gone: %d, want 401", got)
	}
}

func TestJWKSIgnoresUnsafeAndUnsuitableKeys(t *testing.T) {
	testKeys(t)
	p := newProvider(t,
		rsaJWK("weak", &weakRSA.PublicKey, nil),                        // 1024 bits: can be factored
		rsaJWK("enc", &rsaKey.PublicKey, map[string]any{"use": "enc"}), // an encryption key, not a signing key
		rsaJWK("good", &rsaKey2.PublicKey, nil),
	)
	app := build(jwt.Config{JWKSURL: p.srv.URL})
	if got := status(app, issue(t, "RS256", weakRSA, "weak")); got != 401 {
		t.Errorf("a token from a 1024-bit key: %d, want 401", got)
	}
	if got := status(app, issue(t, "RS256", rsaKey, "enc")); got != 401 {
		t.Errorf("a token from an encryption key: %d, want 401", got)
	}
	if got := status(app, issue(t, "RS256", rsaKey2, "good")); got != 200 {
		t.Errorf("the good key must still work: %d", got)
	}
}

func TestJWKSKeyRestrictedToAnAlgorithm(t *testing.T) {
	testKeys(t)
	p := newProvider(t, rsaJWK("k1", &rsaKey.PublicKey, map[string]any{"alg": "RS256"}))
	app := build(jwt.Config{JWKSURL: p.srv.URL})
	if got := status(app, issue(t, "RS256", rsaKey, "k1")); got != 200 {
		t.Errorf("RS256: %d", got)
	}
	if got := status(app, issue(t, "PS256", rsaKey, "k1")); got != 401 {
		t.Errorf("PS256 with a key published for RS256 only: %d, want 401", got)
	}
}

func TestJWKSSupportsECAndEd25519AndTokensWithoutKid(t *testing.T) {
	testKeys(t)
	p := newProvider(t, ecJWK("ec1", &ecKey.PublicKey), edJWK("ed1", edPub))
	app := build(jwt.Config{JWKSURL: p.srv.URL})
	if got := status(app, issue(t, "ES256", ecKey, "ec1")); got != 200 {
		t.Errorf("ES256 through the JWKS: %d", got)
	}
	if got := status(app, issue(t, "EdDSA", edKey, "ed1")); got != 200 {
		t.Errorf("EdDSA through the JWKS: %d", got)
	}
	if got := status(app, issue(t, "ES256", ecKey, "")); got != 401 {
		t.Errorf("no kid and two keys is ambiguous: %d, want 401", got)
	}

	single := newProvider(t, ecJWK("", &ecKey.PublicKey))
	if got := status(build(jwt.Config{JWKSURL: single.srv.URL}), issue(t, "ES256", ecKey, "")); got != 200 {
		t.Errorf("no kid with exactly one key is fine: %d", got)
	}
}

func TestJWKSURLMustBeHTTPSExceptForLocalhost(t *testing.T) {
	for _, bad := range []string{"http://keys.example.com/jwks.json", "ftp://x/y", "not a url", "https://"} {
		if _, err := jwt.NewJWKS(bad); err == nil {
			t.Errorf("NewJWKS(%q) must be refused", bad)
		}
	}
	for _, ok := range []string{"https://keys.example.com/jwks.json", "http://localhost:8080/jwks", "http://127.0.0.1/jwks"} {
		if _, err := jwt.NewJWKS(ok); err != nil {
			t.Errorf("NewJWKS(%q) = %v", ok, err)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("Middleware must panic for a plain http JWKS URL")
		}
	}()
	jwt.Middleware(jwt.Config{JWKSURL: "http://keys.example.com/jwks.json"})
}

func TestIssuerAudienceAndLeeway(t *testing.T) {
	testKeys(t)
	sign := func(o jwt.IssueOptions) string {
		o.TTL = time.Hour
		tok, err := jwt.Issue("RS256", rsaKey, o)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	app := build(jwt.Config{PublicKey: &rsaKey.PublicKey, Issuer: "https://auth.example.com", Audience: "my-api"})
	good := sign(jwt.IssueOptions{Subject: "u", Issuer: "https://auth.example.com", Audience: "my-api"})
	if got := status(app, good); got != 200 {
		t.Fatalf("matching issuer and audience: %d", got)
	}
	for name, tok := range map[string]string{
		"wrong issuer":   sign(jwt.IssueOptions{Subject: "u", Issuer: "https://evil.example.com", Audience: "my-api"}),
		"wrong audience": sign(jwt.IssueOptions{Subject: "u", Issuer: "https://auth.example.com", Audience: "another-api"}),
		"no audience":    sign(jwt.IssueOptions{Subject: "u", Issuer: "https://auth.example.com"}),
		"no issuer":      sign(jwt.IssueOptions{Subject: "u", Audience: "my-api"}),
	} {
		if got := status(app, tok); got != 401 {
			t.Errorf("%s: %d, want 401", name, got)
		}
	}

	// A token that expired 10 seconds ago is accepted only with leeway.
	expired, _ := gojwt.NewWithClaims(gojwt.SigningMethodRS256, gojwt.MapClaims{"sub": "u", "exp": time.Now().Add(-10 * time.Second).Unix()}).SignedString(rsaKey)
	if got := status(build(jwt.Config{PublicKey: &rsaKey.PublicKey}), expired); got != 401 {
		t.Errorf("expired without leeway: %d, want 401", got)
	}
	if got := status(build(jwt.Config{PublicKey: &rsaKey.PublicKey, Leeway: 30 * time.Second}), expired); got != 200 {
		t.Errorf("expired within the leeway: %d, want 200", got)
	}
}

func TestMisconfigurationPanicsAtStartup(t *testing.T) {
	testKeys(t)
	for name, cfg := range map[string]jwt.Config{
		"nothing set":           {},
		"two sources":           {SecretKey: "s", PublicKey: &rsaKey.PublicKey},
		"secret and jwks":       {SecretKey: "s", JWKSURL: "https://keys.example.com/jwks"},
		"unsupported key type":  {PublicKey: "a string is not a key"},
		"private key as public": {PublicKey: rsaKey},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: expected a panic", name)
				}
			}()
			jwt.Middleware(cfg)
		}()
	}
}

func TestIssue(t *testing.T) {
	testKeys(t)
	if _, err := jwt.Issue("RS256", rsaKey, jwt.IssueOptions{Subject: "u"}); err == nil {
		t.Error("a token without a TTL must not be issued: it would never expire")
	}
	if _, err := jwt.Issue("none", nil, jwt.IssueOptions{TTL: time.Hour}); err == nil {
		t.Error("unsigned tokens must not be issued")
	}
	if _, err := jwt.Issue("XX999", rsaKey, jwt.IssueOptions{TTL: time.Hour}); err == nil {
		t.Error("an unknown algorithm must be refused")
	}
	if _, err := jwt.Issue("RS256", []byte("a secret"), jwt.IssueOptions{TTL: time.Hour}); err == nil {
		t.Error("a key of the wrong type must be refused")
	}

	tok, err := jwt.Issue("HS256", []byte("s3cret"), jwt.IssueOptions{
		Subject: "user-7", TTL: time.Hour, KeyID: "main",
		Claims: map[string]any{"role": "admin", "exp": 9999999999, "sub": "someone-else"},
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _, _ := gojwt.NewParser().ParseUnverified(tok, gojwt.MapClaims{})
	claims := parsed.Claims.(gojwt.MapClaims)
	if claims["sub"] != "user-7" || claims["role"] != "admin" || parsed.Header["kid"] != "main" {
		t.Errorf("claims = %v, header = %v", claims, parsed.Header)
	}
	if exp, _ := claims["exp"].(float64); exp > float64(time.Now().Add(2*time.Hour).Unix()) {
		t.Errorf("a caller must not be able to override exp: %v", claims["exp"])
	}
	if got := status(build(jwt.Config{SecretKey: "s3cret"}), tok); got != 200 {
		t.Errorf("a token we issued must be accepted by the middleware: %d", got)
	}
}
