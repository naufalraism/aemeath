// Package api is the shared contract: the types and endpoints are used by
// both the server and the Go client.
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
	Name  string `json:"name" validate:"required,min=2,max=50" description:"Display name" example:"Aemeath"`
	Email string `json:"email" validate:"required,email" example:"aemeath@example.com"`
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
