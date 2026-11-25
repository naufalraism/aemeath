package jwt_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/jwt"
)

// The verified identity used to live in the per-request values (then Ctx.State) under "user_id" and
// "jwt_claims", where any application code could overwrite it: a
// middleware keeping "user_id" in the per-request values for an audit log, filled from the
// query, made jwt.UserID report "admin" for alice's token. UserID and
// Claims now report only what the token says.
func TestUserIDCannotBeOverwrittenThroughState(t *testing.T) {
	secret := strings.Repeat("s", 32)
	exp := time.Now().Add(time.Hour).Unix()
	sign := func(claims gojwt.MapClaims) string {
		s, err := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims).SignedString([]byte(secret))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	spoof := func(next aemeath.HandlerFunc) aemeath.HandlerFunc {
		return func(c *aemeath.Ctx) error {
			c.Set("user_id", c.Query("as"))
			c.Set("jwt_claims", gojwt.MapClaims{"sub": c.Query("as"), "role": "admin"})
			return next(c)
		}
	}
	for _, tc := range []struct {
		name, token, wantUser, wantRole string
		spoofBeforeJWT                  bool
	}{
		{"spoof after jwt", sign(gojwt.MapClaims{"sub": "alice", "exp": exp}), "alice", "", false},
		{"spoof before jwt", sign(gojwt.MapClaims{"sub": "alice", "exp": exp}), "alice", "", true},
		{"token without sub", sign(gojwt.MapClaims{"exp": exp}), "", "", true},
	} {
		app := aemeath.New()
		if tc.spoofBeforeJWT {
			app.Use(spoof, jwt.Middleware(jwt.Config{SecretKey: secret}))
		} else {
			app.Use(jwt.Middleware(jwt.Config{SecretKey: secret}), spoof)
		}
		app.Handle("GET", "/me", func(c *aemeath.Ctx) error {
			claims, _ := jwt.Claims(c)
			role, _ := claims["role"].(string)
			return c.Text(200, jwt.UserID(c)+"|"+role)
		})
		r := httptest.NewRequest("GET", "/me?as=admin", nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if got, want := w.Body.String(), tc.wantUser+"|"+tc.wantRole; got != want {
			t.Errorf("%s: UserID|role = %q, want %q", tc.name, got, want)
		}
	}
}
