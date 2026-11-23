// Package jwt verifies JSON Web Tokens sent as Bearer tokens, signed with a
// shared secret (HS256/384/512), a public key (RS256, ES256, EdDSA, ...) or the
// keys a provider publishes at a JWKS URL, and can issue tokens too.
package jwt

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"

	"github.com/naufalraism/aemeath"
)

// verified is what Middleware stores in the request context for Claims and
// UserID. The key is a private type, so no other code can set or replace it
// (it used to live in Ctx.State under "user_id", which application code
// could overwrite).
type verified struct{ claims map[string]any }

type verifiedKey struct{}

// Config configures Middleware. Set exactly one of SecretKey, PublicKey, JWKSURL or JWKS.
type Config struct {
	// SecretKey verifies HMAC (HS256/384/512) tokens. It must be at least 32
	// bytes (MinSecretBytes); generate one with `openssl rand -base64 32`.
	SecretKey string

	// PublicKey verifies tokens signed with the matching private key: an
	// *rsa.PublicKey (RS256/384/512, PS256/384/512), an *ecdsa.PublicKey (ES256/384/512)
	// or an ed25519.PublicKey (EdDSA). Only the algorithms that fit the key are accepted,
	// so a token cannot be forged by "signing" it with the public key as an HMAC secret.
	PublicKey crypto.PublicKey

	// JWKSURL fetches the provider's public keys (for example
	// https://example.auth0.com/.well-known/jwks.json). The key is chosen by the
	// token's "kid" and keys are cached and refreshed after a rotation.
	JWKSURL string

	// JWKS is a key set you share with other code, instead of JWKSURL.
	JWKS *JWKS

	// Issuer, if set, must equal the token's "iss" claim.
	Issuer string

	// Audience, if set, must be among the token's "aud" claim.
	Audience string

	// Leeway tolerates a little clock difference between servers (for "exp", "nbf", "iat").
	Leeway time.Duration

	// AllowNoExpiry accepts tokens that have no "exp" claim. By default such
	// tokens are rejected, because they would never expire.
	AllowNoExpiry bool
}

func methodsForKey(pub crypto.PublicKey) []string {
	switch k := pub.(type) {
	case *rsa.PublicKey:
		return []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512"}
	case *ecdsa.PublicKey:
		switch k.Curve {
		case elliptic.P256():
			return []string{"ES256"}
		case elliptic.P384():
			return []string{"ES384"}
		case elliptic.P521():
			return []string{"ES512"}
		}
	case ed25519.PublicKey:
		return []string{"EdDSA"}
	}
	return nil
}

// MinSecretBytes is the shortest HMAC secret Middleware, NewVerifier and Issue
// accept: 32 bytes, the output size of SHA-256 (RFC 7518, section 3.2).
const MinSecretBytes = 32

var asymmetricMethods = []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA"}

// Middleware verifies Bearer tokens. It panics at startup when the key
// configuration is wrong (none, several, an empty secret, an unsupported key):
// a mistake here must never turn into tokens that anyone can forge.
// newParser returns the parser and, for a request's context, the function that
// finds the verification key. The context matters for a JWKS: the key lookup
// stops waiting when the request ends.
func newParser(cfg Config) (*gojwt.Parser, func(context.Context) gojwt.Keyfunc) {
	sources := 0
	for _, set := range []bool{cfg.SecretKey != "", cfg.PublicKey != nil, cfg.JWKSURL != "", cfg.JWKS != nil} {
		if set {
			sources++
		}
	}
	if sources != 1 {
		panic("aemeath/jwt: set exactly one of Config.SecretKey, PublicKey, JWKSURL or JWKS (a missing or empty SecretKey would let anyone forge tokens)")
	}

	var (
		methods []string
		keyFunc func(context.Context) gojwt.Keyfunc
	)
	switch {
	case cfg.SecretKey != "":
		if len(cfg.SecretKey) < MinSecretBytes {
			panic(fmt.Sprintf("aemeath/jwt: Config.SecretKey is %d bytes; HMAC secrets must be at least %d random bytes, or anyone holding one token can brute-force it offline (generate one: openssl rand -base64 32)", len(cfg.SecretKey), MinSecretBytes))
		}
		key := []byte(cfg.SecretKey)
		methods = []string{"HS256", "HS384", "HS512"}
		keyFunc = func(context.Context) gojwt.Keyfunc { return func(*gojwt.Token) (any, error) { return key, nil } }
	case cfg.PublicKey != nil:
		methods = methodsForKey(cfg.PublicKey)
		if len(methods) == 0 {
			panic(fmt.Sprintf("aemeath/jwt: unsupported Config.PublicKey type %T", cfg.PublicKey))
		}
		pub := cfg.PublicKey
		keyFunc = func(context.Context) gojwt.Keyfunc { return func(*gojwt.Token) (any, error) { return pub, nil } }
	default:
		set := cfg.JWKS
		if set == nil {
			var err error
			if set, err = NewJWKS(cfg.JWKSURL); err != nil {
				panic("aemeath/jwt: " + err.Error())
			}
		}
		methods = asymmetricMethods
		keyFunc = func(ctx context.Context) gojwt.Keyfunc {
			return func(t *gojwt.Token) (any, error) {
				kid, _ := t.Header["kid"].(string)
				key, alg, err := set.Key(ctx, kid)
				if err != nil {
					return nil, err
				}
				if alg != "" && alg != t.Method.Alg() {
					return nil, fmt.Errorf("jwt: the key is restricted to %s but the token uses %s", alg, t.Method.Alg())
				}
				return key, nil
			}
		}
	}

	opts := []gojwt.ParserOption{gojwt.WithValidMethods(methods)}
	if !cfg.AllowNoExpiry {
		opts = append(opts, gojwt.WithExpirationRequired())
	}
	if cfg.Issuer != "" {
		opts = append(opts, gojwt.WithIssuer(cfg.Issuer))
	}
	if cfg.Audience != "" {
		opts = append(opts, gojwt.WithAudience(cfg.Audience))
	}
	if cfg.Leeway > 0 {
		opts = append(opts, gojwt.WithLeeway(cfg.Leeway))
	}
	parser := gojwt.NewParser(opts...)
	return parser, keyFunc
}

func Middleware(cfg Config) aemeath.Middleware {
	parser, keyFunc := newParser(cfg)

	unauthorized := func(c *aemeath.Ctx, msg string) error {
		c.W.Header().Set("WWW-Authenticate", "Bearer")
		return aemeath.NewError(http.StatusUnauthorized, msg)
	}

	return func(next aemeath.HandlerFunc) aemeath.HandlerFunc {
		return func(c *aemeath.Ctx) error {
			scheme, tokenString, ok := strings.Cut(c.R.Header.Get("Authorization"), " ")
			if !ok || !strings.EqualFold(scheme, "Bearer") || tokenString == "" {
				return unauthorized(c, "missing token or invalid Authorization format")
			}

			token, err := parser.Parse(tokenString, keyFunc(c.Context()))
			if err != nil || !token.Valid {
				return unauthorized(c, "invalid or expired token")
			}

			c.MarkAuthenticated()
			if claims, ok := token.Claims.(gojwt.MapClaims); ok {
				c.R = c.R.WithContext(context.WithValue(c.R.Context(), verifiedKey{}, verified{map[string]any(claims)}))
			}
			return next(c)
		}
	}
}

// Claims returns the verified token claims of the current request. The map
// is the decoded JSON payload: strings, float64 numbers, bools, []any and
// map[string]any.
func Claims(c *aemeath.Ctx) (map[string]any, bool) {
	v, ok := c.Context().Value(verifiedKey{}).(verified)
	return v.claims, ok
}

// UserID returns the "sub" claim of the verified token as a string ("" if absent).
// Numeric subjects are rendered without a fractional part.
func UserID(c *aemeath.Ctx) string {
	claims, _ := Claims(c)
	switch v := claims["sub"].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// Verifier checks tokens outside HTTP middleware (for example an OpenID Connect
// ID token). It applies exactly the rules of Config, including the algorithm
// restrictions that defeat algorithm-confusion attacks.
type Verifier struct {
	parser  *gojwt.Parser
	keyFunc func(context.Context) gojwt.Keyfunc
}

// NewVerifier builds a Verifier. It panics on a bad key configuration, like Middleware.
func NewVerifier(cfg Config) *Verifier {
	p, k := newParser(cfg)
	return &Verifier{p, k}
}

// Verify checks the signature and the registered claims and returns the claims.
// It is VerifyContext with context.Background().
func (v *Verifier) Verify(token string) (map[string]any, error) {
	return v.VerifyContext(context.Background(), token)
}

// VerifyContext is Verify for a request: with a JWKS, the key lookup stops
// waiting when ctx ends.
func (v *Verifier) VerifyContext(ctx context.Context, token string) (map[string]any, error) {
	t, err := v.parser.Parse(token, v.keyFunc(ctx))
	if err != nil || !t.Valid {
		if err == nil {
			err = errors.New("invalid token")
		}
		return nil, err
	}
	claims, _ := t.Claims.(gojwt.MapClaims)
	return map[string]any(claims), nil
}
