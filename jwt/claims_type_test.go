package jwt_test

import (
	"context"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/jwt"
)

// The claims are a plain map, so the public API does not depend on the JWT
// library underneath (golang-jwt today): it can be upgraded or replaced
// without breaking applications. Reading a claim is unchanged:
// claims["role"].(string).
var (
	_ func(*aemeath.Ctx) (map[string]any, bool)                            = jwt.Claims
	_ func(*jwt.Verifier, string) (map[string]any, error)                  = (*jwt.Verifier).Verify
	_ func(*jwt.Verifier, context.Context, string) (map[string]any, error) = (*jwt.Verifier).VerifyContext
)
