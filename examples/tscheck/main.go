// Command tscheck is the server used by CI to verify the generated TypeScript:
//
//	go run ./examples/tscheck -write examples/tscheck/generated   # write client.ts, schemas.ts, hooks.ts
//	go run ./examples/tscheck                                     # serve on :38123
//
// examples/tscheck/verify.ts then type-checks the generated files and checks
// that the generated Zod schemas make the same accept/reject decisions as the server.
package main

import (
	"flag"
	"log"

	"github.com/naufalraism/aemeath"
)

type Address struct {
	City string `json:"city" validate:"required,min=2"`
}
type SignupIn struct {
	Name     string   `json:"name" validate:"required,min=3,max=10" description:"Display name" example:"Bob"`
	Email    string   `json:"email" validate:"required,email"`
	Role     string   `json:"role" validate:"oneof=admin user"`
	Age      *int     `json:"age" validate:"required,gte=18,lt=120"`
	Level    int      `json:"level" validate:"oneof=1 2 3"`
	Tags     []string `json:"tags" validate:"max=2"`
	Code     string   `json:"code" validate:"pattern=^[A-Z]{3}-[0-9]+$"`
	Address  Address  `json:"address"`
	Nickname *string  `json:"nickname"`
}
type User struct {
	ID       int      `json:"id"`
	Name     string   `json:"name"`
	Tags     []string `json:"tags"`
	Nickname *string  `json:"nickname"`
	Friends  []*User  `json:"friends,omitempty"`
}
type ListIn struct {
	Tags  []string `query:"tag" validate:"max=3"`
	Limit *int     `query:"limit" validate:"max=100"`
	Trace string   `header:"X-Trace"`
}
type ByID struct {
	ID int `path:"id"`
}

func main() {
	write := flag.String("write", "", "write the generated TypeScript into this directory and exit")
	flag.Parse()

	app := aemeath.New()
	app.Use(aemeath.RequestID(), aemeath.Logger(), aemeath.CORS())
	aemeath.GET(app, "/items", func(c *aemeath.Ctx, in ListIn) ([]User, error) { return []User{{ID: 1, Tags: in.Tags}}, nil })
	aemeath.POST(app, "/signup", func(c *aemeath.Ctx, in SignupIn) (User, error) { return User{ID: 7, Name: in.Name}, nil }, aemeath.Status(201))
	aemeath.GET(app, "/items/{id}", func(c *aemeath.Ctx, in ByID) (User, error) { return User{ID: in.ID}, nil })
	aemeath.DELETE(app, "/items/{id}", func(c *aemeath.Ctx, in ByID) (struct{}, error) { return struct{}{}, nil }, aemeath.Status(204))
	aemeath.GET(app, "/ping", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) { return map[string]string{"ok": "1"}, nil })
	app.EnableClient()
	app.EnableDocs(aemeath.Info{Title: "TypeScript check"})
	if *write != "" {
		if err := app.WriteTypeScript(*write); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := app.Listen(":38123"); err != nil {
		log.Fatal(err)
	}
}
