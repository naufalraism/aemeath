package jwt_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
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
	app := build(jwt.Config{SecretKey: "s3cret-0123456789-abcdefghijklmnop"})
	tok := sign(t, "s3cret-0123456789-abcdefghijklmnop", gojwt.MapClaims{"sub": "user-1", "role": "admin", "exp": time.Now().Add(time.Hour).Unix()}, gojwt.SigningMethodHS256)

	rec := call(app, "Bearer "+tok)
	if rec.Code != 200 || rec.Body.String() != "{\"role\":\"admin\",\"user\":\"user-1\"}\n" {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body)
	}
	if rec := call(app, "bearer "+tok); rec.Code != 200 {
		t.Fatalf("the scheme is case-insensitive: status = %d", rec.Code)
	}
}

func TestNumericSubject(t *testing.T) {
	app := build(jwt.Config{SecretKey: "s3cret-0123456789-abcdefghijklmnop"})
	tok := sign(t, "s3cret-0123456789-abcdefghijklmnop", gojwt.MapClaims{"sub": 42, "exp": time.Now().Add(time.Hour).Unix()}, gojwt.SigningMethodHS256)
	if rec := call(app, "Bearer "+tok); rec.Body.String() != "{\"role\":null,\"user\":\"42\"}\n" {
		t.Fatalf("body = %q", rec.Body)
	}
}

func TestRejections(t *testing.T) {
	app := build(jwt.Config{SecretKey: "s3cret-0123456789-abcdefghijklmnop"})
	exp := time.Now().Add(time.Hour).Unix()
	for name, header := range map[string]string{
		"no header":        "",
		"wrong scheme":     "Basic abc",
		"no token":         "Bearer ",
		"garbage":          "Bearer not.a.jwt",
		"wrong secret":     "Bearer " + sign(t, "other", gojwt.MapClaims{"exp": exp}, gojwt.SigningMethodHS256),
		"expired":          "Bearer " + sign(t, "s3cret-0123456789-abcdefghijklmnop", gojwt.MapClaims{"exp": time.Now().Add(-time.Hour).Unix()}, gojwt.SigningMethodHS256),
		"no exp (default)": "Bearer " + sign(t, "s3cret-0123456789-abcdefghijklmnop", gojwt.MapClaims{"sub": "u"}, gojwt.SigningMethodHS256),
	} {
		rec := call(app, header)
		if rec.Code != 401 || rec.Header().Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("%s: status = %d, WWW-Authenticate = %q", name, rec.Code, rec.Header().Get("WWW-Authenticate"))
		}
	}
}

func TestAlgorithmNoneIsRejected(t *testing.T) {
	app := build(jwt.Config{SecretKey: "s3cret-0123456789-abcdefghijklmnop"})
	tok, err := gojwt.NewWithClaims(gojwt.SigningMethodNone, gojwt.MapClaims{"sub": "attacker", "exp": time.Now().Add(time.Hour).Unix()}).SignedString(gojwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if rec := call(app, "Bearer "+tok); rec.Code != 401 {
		t.Fatalf("an unsigned token must be rejected, got %d", rec.Code)
	}
}

func TestAllowNoExpiry(t *testing.T) {
	tok := sign(t, "s3cret-0123456789-abcdefghijklmnop", gojwt.MapClaims{"sub": "u"}, gojwt.SigningMethodHS256)
	if rec := call(build(jwt.Config{SecretKey: "s3cret-0123456789-abcdefghijklmnop", AllowNoExpiry: true}), "Bearer "+tok); rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// An HS256 secret such as "secret" can be brute-forced offline from any one
// token; it used to be accepted without a word.
func TestShortSecretsAreRejected(t *testing.T) {
	for _, s := range []string{"secret", "changeme", strings.Repeat("x", jwt.MinSecretBytes-1)} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Middleware accepted a %d-byte secret", len(s))
				}
			}()
			jwt.Middleware(jwt.Config{SecretKey: s})
		}()
		if _, err := jwt.Issue("HS256", []byte(s), jwt.IssueOptions{TTL: time.Minute}); err == nil {
			t.Errorf("Issue accepted a %d-byte secret", len(s))
		}
	}
	jwt.Middleware(jwt.Config{SecretKey: strings.Repeat("x", jwt.MinSecretBytes)}) // must not panic
}

// A valid token marks the request, so routes with RequireAuth accept it.
func TestMiddlewareSatisfiesRequireAuth(t *testing.T) {
	const secret = "s3cret-0123456789-abcdefghijklmnop"
	app := aemeath.New()
	g := app.Group("/api", jwt.Middleware(jwt.Config{SecretKey: secret}))
	g.Options(aemeath.RequireAuth())
	aemeath.GroupGET(g, "/me", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
		return map[string]string{"user": jwt.UserID(c)}, nil
	})
	tok := sign(t, secret, gojwt.MapClaims{"sub": "u1", "exp": time.Now().Add(time.Hour).Unix()}, gojwt.SigningMethodHS256)
	r := httptest.NewRequest("GET", "/api/me", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "u1") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
