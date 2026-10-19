package main

import (
	"log"
	"os"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/examples/api"
)

func main() {
	app := aemeath.New()

	// 1. CORS Global
	app.Use(aemeath.Recover(), aemeath.Logger(), aemeath.CORS())

	// Tambahkan ini untuk menyajikan folder "public" (pastikan kamu punya folder public di root, isi dengan index.html)
	app.Static("/public", "./public")

	// 2. Mendaftarkan route default
	aemeath.Register(app, api.Hello, func(c *aemeath.Ctx, in api.HelloIn) (api.HelloOut, error) {
		greeting := "Halo"
		if in.Lang == "en" {
			greeting = "Hello"
		}
		return api.HelloOut{Message: greeting + ", " + in.Name}, nil
	}, aemeath.Summary("Menyapa"), aemeath.OperationID("hello"))

	aemeath.Register(app, api.CreateUser, func(c *aemeath.Ctx, in api.CreateUserIn) (api.User, error) {
		return api.User{ID: 1, Name: in.Name, Email: in.Email}, nil
	}, aemeath.Summary("Buat user"), aemeath.Status(201))

	// 3. Fitur Route Group (Gunakan GroupGET, bukan adminGroup.GET)
	adminGroup := app.Group("/admin", func(next aemeath.HandlerFunc) aemeath.HandlerFunc {
		return func(c *aemeath.Ctx) error {
			log.Println("Masuk ke area Admin")
			return next(c)
		}
	})

	// INI YANG DIRUBAH: pakai aemeath.GroupGET
	aemeath.GroupGET(adminGroup, "/dashboard", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
		return map[string]string{"status": "Welcome to Admin Dashboard"}, nil
	})

	app.EnableDocs(aemeath.Info{Title: "Aemeath Demo", Version: "0.1.0"})
	app.EnableClient()

	if len(os.Args) > 1 && os.Args[1] == "gen" {
		if err := app.WriteTypeScriptClient("client.ts"); err != nil {
			log.Fatal(err)
		}
		log.Println("client.ts dibuat")
		return
	}

	log.Println("jalan di http://localhost:3000 (docs: /docs, client: /client.ts)")
	log.Fatal(app.Listen(":3000"))
}
