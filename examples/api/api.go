// Package api adalah kontrak bersama: tipe dan endpoint dipakai oleh server dan client.
package api

import "github.com/naufalraism/aemeath"

type HelloIn struct {
	Name string `path:"name"`
	Lang string `query:"lang"`
}

type HelloOut struct {
	Message string `json:"message"`
}

type CreateUserIn struct {
	Name  string `json:"name" validate:"required"`
	Email string `json:"email" validate:"required"`
}

type User struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

var (
	Hello      = aemeath.NewEndpoint[HelloIn, HelloOut]("GET", "/api/v1/hello/{name}")
	CreateUser = aemeath.NewEndpoint[CreateUserIn, User]("POST", "/api/v1/users")
)
