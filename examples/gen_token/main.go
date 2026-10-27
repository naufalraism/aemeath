// Command gen_token prints a JWT that the basic example accepts.
//
//	JWT_SECRET=your-secret go run ./examples/gen_token
//
// Without JWT_SECRET it uses the same development secret as the basic example.
package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const devSecret = "dev-only-secret-change-me"

func main() {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = devSecret
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user-123",
		"exp": time.Now().Add(24 * time.Hour).Unix(),
	})
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(signed)
}
