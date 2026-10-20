package main

import (
	"log"
	"os"
	"time"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/examples/api"
	"gorm.io/gorm" // <-- Import GORM untuk database
)

func main() {
	app := aemeath.New()

	// 1. Middleware Global & Static File
	app.Use(aemeath.Recover(), aemeath.Logger(), aemeath.CORS())
	app.Static("/public", "./public")

	// 2. Route Default
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

	// 3. Route Group (Admin)
	adminGroup := app.Group("/admin", func(next aemeath.HandlerFunc) aemeath.HandlerFunc {
		return func(c *aemeath.Ctx) error {
			log.Println("Masuk ke area Admin")
			return next(c)
		}
	})
	aemeath.GroupGET(adminGroup, "/dashboard", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
		return map[string]string{"status": "Welcome to Admin Dashboard"}, nil
	})

	// 4. FITUR DATABASE (GORM)
	// Mendaftarkan plugin database (akan membuat file aemeath.db secara otomatis)
	dbPlugin := aemeath.NewDBPlugin("aemeath.db")
	app.RegisterPlugin(dbPlugin)

	// 5. Fitur JWT Auth & Rate Limiter
	jwtAuth := aemeath.JWTMiddleware(aemeath.JWTConfig{
		SecretKey: "rahasia-negara-aemeath",
	})

	secretGroup := app.Group("/api/secret")
	secretGroup.Use(jwtAuth, aemeath.RateLimiter(3, 1*time.Minute))

	aemeath.GroupGET(secretGroup, "/profile", func(c *aemeath.Ctx, _ struct{}) (map[string]any, error) {
		userID := c.State["user_id"]
		return map[string]any{
			"message": "Halo! Kamu berhasil masuk.",
			"user_id": userID,
		}, nil
	})

	// Rute Database: Simpan User Baru
	aemeath.GroupPOST(secretGroup, "/users", func(c *aemeath.Ctx, in api.CreateUserIn) (aemeath.User, error) {
		db := c.State["db"].(*gorm.DB) // Ambil DB dari State middleware
		user := aemeath.User{Name: in.Name, Email: in.Email}

		if err := db.Create(&user).Error; err != nil {
			return user, aemeath.NewError(400, "Gagal simpan, email mungkin sudah dipakai")
		}
		return user, nil
	})

	// Rute Database: Ambil Semua User
	aemeath.GroupGET(secretGroup, "/users", func(c *aemeath.Ctx, _ struct{}) ([]aemeath.User, error) {
		db := c.State["db"].(*gorm.DB)
		var users []aemeath.User
		db.Find(&users) // Ambil semua data
		return users, nil
	})

	// 6. Fitur WebSocket (Echo Server)
	app.Handle("GET", "/ws", func(c *aemeath.Ctx) error {
		conn, err := c.Upgrade()
		if err != nil {
			return err
		}
		defer conn.Close()

		for {
			mt, message, err := conn.ReadMessage()
			if err != nil {
				break
			}
			if err := conn.WriteMessage(mt, message); err != nil {
				break
			}
		}
		return nil
	})

	// 7. Dokumentasi & Client Generator
	app.EnableDocs(aemeath.Info{Title: "Aemeath Demo", Version: "0.1.0"})
	app.EnableClient()

	if len(os.Args) > 1 && os.Args[1] == "gen" {
		if err := app.WriteTypeScriptClient("client.ts"); err != nil {
			log.Fatal(err)
		}
		log.Println("client.ts dibuat")
		return
	}

	log.Println("jalan di http://localhost:3000 (docs: /docs, client: /client.ts, ws: /ws)")
	log.Fatal(app.Listen(":3000"))
}
