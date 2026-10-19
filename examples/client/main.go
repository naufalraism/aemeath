package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/examples/api"
)

func main() {
	ctx := context.Background()
	c := aemeath.NewClient("http://localhost:3000")

	out, err := api.Hello.Call(ctx, c, api.HelloIn{Name: "Aemeath", Lang: "en"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(out.Message)

	user, err := api.CreateUser.Call(ctx, c, api.CreateUserIn{Name: "Aemeath", Email: "a@example.com"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%+v\n", user)

	_, err = api.CreateUser.Call(ctx, c, api.CreateUserIn{})
	var he *aemeath.HTTPError
	if errors.As(err, &he) {
		fmt.Println("ditolak server:", he.Code, he.Message)
	}
}
