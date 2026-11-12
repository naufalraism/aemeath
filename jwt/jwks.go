package jwt

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// minRSABits is the smallest RSA key accepted from a JWKS: shorter keys can be factored.
const minRSABits = 2048

// ErrKeyNotFound means no key with the requested id is published by the JWKS endpoint.
var ErrKeyNotFound = errors.New("jwt: no matching key in the JWKS")

// JWKS fetches and caches the public keys a provider publishes (a "JSON Web Key
// Set"), so tokens signed by it can be verified. Keys are cached for a while, a
// key id that is not known triggers a refresh (providers rotate keys), and
// refreshes are rate limited so a stream of bogus tokens cannot make the server
// hammer the provider. If a refresh fails, the cached keys keep working.
type JWKS struct {
	url        string
	client     *http.Client
	ttl        time.Duration
	minRefresh time.Duration
	now        func() time.Time

	mu          sync.Mutex
	keys        map[string]jwk
	fetched     time.Time
	lastAttempt time.Time
}

type jwk struct {
	key crypto.PublicKey
	alg string
}

// JWKSOption configures NewJWKS.
type JWKSOption func(*JWKS)

// WithJWKSTTL sets how long keys are cached (default 15 minutes).
func WithJWKSTTL(d time.Duration) JWKSOption { return func(j *JWKS) { j.ttl = d } }

// WithJWKSMinRefresh sets the shortest time between two fetches (default 30 seconds).
func WithJWKSMinRefresh(d time.Duration) JWKSOption { return func(j *JWKS) { j.minRefresh = d } }

// WithJWKSHTTPClient replaces the HTTP client (default: a 10 second timeout).
func WithJWKSHTTPClient(c *http.Client) JWKSOption { return func(j *JWKS) { j.client = c } }

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// NewJWKS prepares a key set for the given URL. It must be https, except for
// localhost (tests and local development). Keys are fetched on first use.
func NewJWKS(rawURL string, opts ...JWKSOption) (*JWKS, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("jwt: invalid JWKS URL %q", rawURL)
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !isLoopback(u.Hostname())) {
		return nil, fmt.Errorf("jwt: the JWKS URL must use https (got %q)", u.Scheme)
	}
	j := &JWKS{
		url:        rawURL,
		client:     &http.Client{Timeout: 10 * time.Second},
		ttl:        15 * time.Minute,
		minRefresh: 30 * time.Second,
		now:        time.Now,
	}
	for _, o := range opts {
		o(j)
	}
	return j, nil
}

// Key returns the public key with the given id, and the algorithm the key is
// restricted to ("" when the provider does not say). An empty kid is accepted
// only when the provider publishes exactly one key.
func (j *JWKS) Key(ctx context.Context, kid string) (crypto.PublicKey, string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.fresh() {
		if k, ok := j.lookup(kid); ok {
			return k.key, k.alg, nil
		}
	}
	// Unknown key or stale cache: refresh, but not more often than minRefresh.
	if j.keys == nil || j.now().Sub(j.lastAttempt) >= j.minRefresh {
		j.lastAttempt = j.now()
		if err := j.fetch(ctx); err != nil && j.keys == nil {
			return nil, "", err
		}
	}
	if k, ok := j.lookup(kid); ok {
		return k.key, k.alg, nil
	}
	return nil, "", ErrKeyNotFound
}

func (j *JWKS) fresh() bool { return j.keys != nil && j.now().Sub(j.fetched) < j.ttl }

func (j *JWKS) lookup(kid string) (jwk, bool) {
	if kid == "" {
		if len(j.keys) == 1 {
			for _, k := range j.keys {
				return k, true
			}
		}
		return jwk{}, false
	}
	k, ok := j.keys[kid]
	return k, ok
}

type jwkJSON struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (j *JWKS) fetch(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.url, nil)
	if err != nil {
		return err
	}
	res, err := j.client.Do(req)
	if err != nil {
		return fmt.Errorf("jwt: fetching the JWKS: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("jwt: the JWKS endpoint answered %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	var set struct {
		Keys []jwkJSON `json:"keys"`
	}
	if err := json.Unmarshal(body, &set); err != nil {
		return fmt.Errorf("jwt: the JWKS is not valid JSON: %w", err)
	}
	keys := make(map[string]jwk, len(set.Keys))
	for _, raw := range set.Keys {
		if raw.Use != "" && raw.Use != "sig" {
			continue // an encryption key
		}
		key, err := raw.publicKey()
		if err != nil {
			continue // unsupported or unsafe keys are skipped, never trusted
		}
		keys[raw.Kid] = jwk{key: key, alg: raw.Alg}
	}
	if len(keys) == 0 {
		return errors.New("jwt: the JWKS has no usable signing key")
	}
	j.keys, j.fetched = keys, j.now()
	return nil
}

func b64(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

func (k jwkJSON) publicKey() (crypto.PublicKey, error) {
	switch k.Kty {
	case "RSA":
		n, err := b64(k.N)
		if err != nil {
			return nil, err
		}
		e, err := b64(k.E)
		if err != nil {
			return nil, err
		}
		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		if pub.N.BitLen() < minRSABits || pub.E < 3 {
			return nil, errors.New("weak RSA key")
		}
		return pub, nil
	case "EC":
		var curve elliptic.Curve
		switch k.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, errors.New("unsupported curve")
		}
		x, err := b64(k.X)
		if err != nil {
			return nil, err
		}
		y, err := b64(k.Y)
		if err != nil {
			return nil, err
		}
		size := (curve.Params().BitSize + 7) / 8
		if len(x) > size || len(y) > size {
			return nil, errors.New("bad coordinate length")
		}
		point := make([]byte, 1+2*size)
		point[0] = 4
		copy(point[1+size-len(x):1+size], x)
		copy(point[1+2*size-len(y):], y)
		return ecdsa.ParseUncompressedPublicKey(curve, point) // also checks that the point is on the curve
	case "OKP":
		if k.Crv != "Ed25519" {
			return nil, errors.New("unsupported curve")
		}
		x, err := b64(k.X)
		if err != nil || len(x) != ed25519.PublicKeySize {
			return nil, errors.New("bad Ed25519 key")
		}
		return ed25519.PublicKey(x), nil
	}
	return nil, errors.New("unsupported key type")
}
