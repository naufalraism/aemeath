package aemeath_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/naufalraism/aemeath"
)

func TestJWTMiddleware(t *testing.T) {
	app := aemeath.New()
	secret := "test-secret"

	// Rute yang dilindungi JWT
	group := app.Group("/protected")
	group.Use(aemeath.JWTMiddleware(aemeath.JWTConfig{SecretKey: secret}))
	aemeath.GroupGET(group, "/me", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
		return map[string]string{"status": "ok"}, nil
	})

	// 1. Tes tanpa token (harus 401)
	req := httptest.NewRequest("GET", "/protected/me", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("tanpa token: dapat %d, harusnya 401", rec.Code)
	}

	// 2. Tes dengan token valid (harus 200)
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user-123",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tokenString, _ := token.SignedString([]byte(secret))

	req = httptest.NewRequest("GET", "/protected/me", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dengan token valid: dapat %d, harusnya 200", rec.Code)
	}
}
