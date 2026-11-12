package jwt

import (
	"errors"
	"fmt"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
)

// IssueOptions describes a token for Issue.
type IssueOptions struct {
	Subject  string         // the "sub" claim
	Issuer   string         // the "iss" claim, if set
	Audience string         // the "aud" claim, if set
	Claims   map[string]any // extra claims (they cannot override sub, iss, aud, exp or iat)
	TTL      time.Duration  // how long the token is valid; required
	KeyID    string         // the "kid" header, if set (lets a verifier pick the right key after a rotation)
}

// Issue creates a signed token that Middleware accepts.
//
// alg is "HS256", "HS384" or "HS512" (key is a []byte secret), "RS256", "RS384",
// "RS512", "PS256", "PS384" or "PS512" (key is a *rsa.PrivateKey), "ES256",
// "ES384" or "ES512" (key is a *ecdsa.PrivateKey), or "EdDSA" (key is an
// ed25519.PrivateKey). "exp" is always set: a token that never expires is not offered.
func Issue(alg string, key any, o IssueOptions) (string, error) {
	if alg == "" || alg == "none" {
		return "", errors.New("jwt: unsigned tokens are not supported")
	}
	if o.TTL <= 0 {
		return "", errors.New("jwt: IssueOptions.TTL must be positive")
	}
	method := gojwt.GetSigningMethod(alg)
	if method == nil {
		return "", fmt.Errorf("jwt: unsupported algorithm %q", alg)
	}
	now := time.Now()
	claims := gojwt.MapClaims{}
	for k, v := range o.Claims {
		claims[k] = v
	}
	claims["iat"] = now.Unix()
	claims["exp"] = now.Add(o.TTL).Unix()
	if o.Subject != "" {
		claims["sub"] = o.Subject
	}
	if o.Issuer != "" {
		claims["iss"] = o.Issuer
	}
	if o.Audience != "" {
		claims["aud"] = o.Audience
	}
	tok := gojwt.NewWithClaims(method, claims)
	if o.KeyID != "" {
		tok.Header["kid"] = o.KeyID
	}
	signed, err := tok.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("jwt: signing: %w", err)
	}
	return signed, nil
}
