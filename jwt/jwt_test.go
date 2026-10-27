package jwt_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/jwt"
)

func sign(t *testing.T, secret string, claims gojwt.MapClaims, method gojwt.SigningMethod) string {
	t.Helper()
	s, err := gojwt.NewWithClaims(method, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func build(cfg jwt.Config) *aemeath.App {
	app := aemeath.New()
	app.Use(jwt.Middleware(cfg))
	app.Handle("GET", "/me", func(c *aemeath.Ctx) error {
		claims, _ := jwt.Claims(c)
		return c.JSON(200, map[string]any{"user": jwt.UserID(c), "role": claims["role"]})
	})
	return app
}

func call(app http.Handler, authorization string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/me", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}

func TestRejectsEmptySecret(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic for an empty secret")
		}
	}()
	jwt.Middleware(jwt.Config{})
}

func TestValidToken(t *testing.T) {
	app := build(jwt.Config{SecretKey: "s3cret"})
	tok := sign(t, "s3cret", gojwt.MapClaims{"sub": "user-1", "role": "admin", "exp": time.Now().Add(time.Hour).Unix()}, gojwt.SigningMethodHS256)

	rec := call(app, "Bearer "+tok)
	if rec.Code != 200 || rec.Body.String() != "{\"role\":\"admin\",\"user\":\"user-1\"}\n" {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body)
	}
	if rec := call(app, "bearer "+tok); rec.Code != 200 {
		t.Fatalf("the scheme is case-insensitive: status = %d", rec.Code)
	}
}

func TestNumericSubject(t *testing.T) {
	app := build(jwt.Config{SecretKey: "s3cret"})
	tok := sign(t, "s3cret", gojwt.MapClaims{"sub": 42, "exp": time.Now().Add(time.Hour).Unix()}, gojwt.SigningMethodHS256)
	if rec := call(app, "Bearer "+tok); rec.Body.String() != "{\"role\":null,\"user\":\"42\"}\n" {
		t.Fatalf("body = %q", rec.Body)
	}
}

func TestRejections(t *testing.T) {
	app := build(jwt.Config{SecretKey: "s3cret"})
	exp := time.Now().Add(time.Hour).Unix()
	for name, header := range map[string]string{
		"no header":        "",
		"wrong scheme":     "Basic abc",
		"no token":         "Bearer ",
		"garbage":          "Bearer not.a.jwt",
		"wrong secret":     "Bearer " + sign(t, "other", gojwt.MapClaims{"exp": exp}, gojwt.SigningMethodHS256),
		"expired":          "Bearer " + sign(t, "s3cret", gojwt.MapClaims{"exp": time.Now().Add(-time.Hour).Unix()}, gojwt.SigningMethodHS256),
		"no exp (default)": "Bearer " + sign(t, "s3cret", gojwt.MapClaims{"sub": "u"}, gojwt.SigningMethodHS256),
	} {
		rec := call(app, header)
		if rec.Code != 401 || rec.Header().Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("%s: status = %d, WWW-Authenticate = %q", name, rec.Code, rec.Header().Get("WWW-Authenticate"))
		}
	}
}

func TestAlgorithmNoneIsRejected(t *testing.T) {
	app := build(jwt.Config{SecretKey: "s3cret"})
	tok, err := gojwt.NewWithClaims(gojwt.SigningMethodNone, gojwt.MapClaims{"sub": "attacker", "exp": time.Now().Add(time.Hour).Unix()}).SignedString(gojwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if rec := call(app, "Bearer "+tok); rec.Code != 401 {
		t.Fatalf("an unsigned token must be rejected, got %d", rec.Code)
	}
}

func TestAllowNoExpiry(t *testing.T) {
	tok := sign(t, "s3cret", gojwt.MapClaims{"sub": "u"}, gojwt.SigningMethodHS256)
	if rec := call(build(jwt.Config{SecretKey: "s3cret", AllowNoExpiry: true}), "Bearer "+tok); rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
