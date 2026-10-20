package aemeath

import (
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// JWTConfig adalah konfigurasi untuk plugin JWT.
type JWTConfig struct {
	SecretKey string
}

// JWTMiddleware adalah middleware yang mengecek validitas token.
func JWTMiddleware(cfg JWTConfig) Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) error {
			authHeader := c.R.Header.Get("Authorization")
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				return NewError(http.StatusUnauthorized, "Token tidak ditemukan atau format salah")
			}

			tokenString := strings.TrimPrefix(authHeader, "Bearer ")

			token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
				if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, NewError(http.StatusUnauthorized, "Metode signing tidak valid")
				}
				return []byte(cfg.SecretKey), nil
			})

			if err != nil || !token.Valid {
				return NewError(http.StatusUnauthorized, "Token tidak valid atau kedaluwarsa")
			}

			// Simpan klaim (claims) ke dalam State agar bisa dibaca handler
			if claims, ok := token.Claims.(jwt.MapClaims); ok {
				c.State["jwt_claims"] = claims
				if sub, ok := claims["sub"]; ok {
					c.State["user_id"] = sub
				}
			}

			return next(c)
		}
	}
}
