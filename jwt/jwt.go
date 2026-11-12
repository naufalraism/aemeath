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

// Keys under which Middleware stores data in Ctx.State.
const (
	StateClaims = "jwt_claims" // gojwt.MapClaims
	StateUserID = "user_id"    // the "sub" claim, if present
)

// Config configures Middleware. Set exactly one of SecretKey, PublicKey, JWKSURL or JWKS.
type Config struct {
	// SecretKey verifies HMAC (HS256/384/512) tokens.
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

var asymmetricMethods = []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA"}

// Middleware verifies Bearer tokens. It panics at startup when the key
// configuration is wrong (none, several, an empty secret, an unsupported key):
// a mistake here must never turn into tokens that anyone can forge.
func newParser(cfg Config) (*gojwt.Parser, gojwt.Keyfunc) {
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
		keyFunc gojwt.Keyfunc
	)
	switch {
	case cfg.SecretKey != "":
		key := []byte(cfg.SecretKey)
		methods = []string{"HS256", "HS384", "HS512"}
		keyFunc = func(*gojwt.Token) (any, error) { return key, nil }
	case cfg.PublicKey != nil:
		methods = methodsForKey(cfg.PublicKey)
		if len(methods) == 0 {
			panic(fmt.Sprintf("aemeath/jwt: unsupported Config.PublicKey type %T", cfg.PublicKey))
		}
		pub := cfg.PublicKey
		keyFunc = func(*gojwt.Token) (any, error) { return pub, nil }
	default:
		set := cfg.JWKS
		if set == nil {
			var err error
			if set, err = NewJWKS(cfg.JWKSURL); err != nil {
				panic("aemeath/jwt: " + err.Error())
			}
		}
		methods = asymmetricMethods
		keyFunc = func(t *gojwt.Token) (any, error) {
			kid, _ := t.Header["kid"].(string)
			key, alg, err := set.Key(context.Background(), kid)
			if err != nil {
				return nil, err
			}
			if alg != "" && alg != t.Method.Alg() {
				return nil, fmt.Errorf("jwt: the key is restricted to %s but the token uses %s", alg, t.Method.Alg())
			}
			return key, nil
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

			token, err := parser.Parse(tokenString, keyFunc)
			if err != nil || !token.Valid {
				return unauthorized(c, "invalid or expired token")
			}

			if claims, ok := token.Claims.(gojwt.MapClaims); ok {
				c.State[StateClaims] = claims
				if sub, ok := claims["sub"]; ok {
					c.State[StateUserID] = sub
				}
			}
			return next(c)
		}
	}
}

// Claims returns the verified token claims of the current request.
func Claims(c *aemeath.Ctx) (gojwt.MapClaims, bool) {
	claims, ok := c.State[StateClaims].(gojwt.MapClaims)
	return claims, ok
}

// UserID returns the "sub" claim of the verified token as a string ("" if absent).
// Numeric subjects are rendered without a fractional part.
func UserID(c *aemeath.Ctx) string {
	switch v := c.State[StateUserID].(type) {
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
	keyFunc gojwt.Keyfunc
}

// NewVerifier builds a Verifier. It panics on a bad key configuration, like Middleware.
func NewVerifier(cfg Config) *Verifier {
	p, k := newParser(cfg)
	return &Verifier{p, k}
}

// Verify checks the signature and the registered claims and returns the claims.
func (v *Verifier) Verify(token string) (gojwt.MapClaims, error) {
	t, err := v.parser.Parse(token, v.keyFunc)
	if err != nil || !t.Valid {
		if err == nil {
			err = errors.New("invalid token")
		}
		return nil, err
	}
	claims, _ := t.Claims.(gojwt.MapClaims)
	return claims, nil
}
