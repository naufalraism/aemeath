// Package jwt verifies HMAC-signed JSON Web Tokens sent as Bearer tokens.
package jwt

import (
	"net/http"
	"strconv"
	"strings"

	gojwt "github.com/golang-jwt/jwt/v5"

	"github.com/naufalraism/aemeath"
)

// Keys under which Middleware stores data in Ctx.State.
const (
	StateClaims = "jwt_claims" // gojwt.MapClaims
	StateUserID = "user_id"    // the "sub" claim, if present
)

// Config configures Middleware.
type Config struct {
	// SecretKey is the HMAC secret used to verify tokens. Required.
	SecretKey string

	// AllowNoExpiry accepts tokens that have no "exp" claim. By default such
	// tokens are rejected, because they would never expire.
	AllowNoExpiry bool
}

// Middleware verifies HS256/HS384/HS512 Bearer tokens. It panics at startup
// if SecretKey is empty, since an empty key would let anyone forge tokens.
func Middleware(cfg Config) aemeath.Middleware {
	if cfg.SecretKey == "" {
		panic("aemeath/jwt: Config.SecretKey must not be empty")
	}
	key := []byte(cfg.SecretKey)
	opts := []gojwt.ParserOption{gojwt.WithValidMethods([]string{"HS256", "HS384", "HS512"})}
	if !cfg.AllowNoExpiry {
		opts = append(opts, gojwt.WithExpirationRequired())
	}
	parser := gojwt.NewParser(opts...)

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

			token, err := parser.Parse(tokenString, func(*gojwt.Token) (any, error) { return key, nil })
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
