package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/examples/api"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

func main() {
	app := aemeath.New()

	// 1. Middleware Global
	app.Use(aemeath.Recover(), aemeath.Logger(), aemeath.CORS())

	// 2. Sajikan Landing Page dari folder "website" di URL utama (/)
	app.Static("/", "./website")

	// 3. Route Default
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

	// 4. Route Group (Admin)
	adminGroup := app.Group("/admin", func(next aemeath.HandlerFunc) aemeath.HandlerFunc {
		return func(c *aemeath.Ctx) error {
			log.Println("Masuk ke area Admin")
			return next(c)
		}
	})
	aemeath.GroupGET(adminGroup, "/dashboard", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
		return map[string]string{"status": "Welcome to Admin Dashboard"}, nil
	})

	// 5. FITUR DATABASE (GORM)
	dbPlugin := aemeath.NewDBPlugin("aemeath.db")
	app.RegisterPlugin(dbPlugin)

	// 6. Fitur JWT Auth & Rate Limiter
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

	aemeath.GroupPOST(secretGroup, "/users", func(c *aemeath.Ctx, in api.CreateUserIn) (aemeath.User, error) {
		db := c.State["db"].(*gorm.DB)
		user := aemeath.User{Name: in.Name, Email: in.Email}
		if err := db.Create(&user).Error; err != nil {
			return user, aemeath.NewError(400, "Gagal simpan, email mungkin sudah dipakai")
		}
		return user, nil
	})

	aemeath.GroupGET(secretGroup, "/users", func(c *aemeath.Ctx, _ struct{}) ([]aemeath.User, error) {
		db := c.State["db"].(*gorm.DB)
		var users []aemeath.User
		db.Find(&users)
		return users, nil
	})

	// 7. Fitur WebSocket
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

	// 8. FITUR PLUGIN MAILER
	mailer := aemeath.NewMailer(
		"localhost", 1025, "user", "pass", "Admin <admin@aemeath.dev>",
	)
	app.RegisterPlugin(mailer)

	aemeath.GET(app, "/send-email", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
		m := c.State["mailer"].(*aemeath.MailerPlugin)
		err := m.Send("pelanggan@example.com", "Halo dari Aemeath!", "<h1>Selamat!</h1>")
		if err != nil {
			log.Println("Gagal kirim email:", err)
			return map[string]string{"status": "gagal, cek terminal"}, nil
		}
		return map[string]string{"status": "email terkirim!"}, nil
	})

	// 9. FITUR PLUGIN CACHE (REDIS)
	cache := aemeath.NewCache("localhost:6379", "", 0)
	app.RegisterPlugin(cache)

	aemeath.GET(app, "/cache-test", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
		redisClient := c.State["cache"].(*redis.Client)
		ctx := context.Background()
		key := "hit_counter"

		val, err := redisClient.Get(ctx, key).Result()
		if err == redis.Nil {
			redisClient.Set(ctx, key, "1", 10*time.Minute)
			return map[string]string{"message": "Cache dibuat!", "value": "1"}, nil
		} else if err != nil {
			return nil, aemeath.InternalError("Gagal akses Redis")
		}
		return map[string]string{"message": "Data dari cache", "value": val}, nil
	})

	// 10. FITUR PLUGIN STORAGE (UPLOAD FILE - INTERFACE)
	// Kita pakai Local Storage untuk testing
	localStore := aemeath.NewLocalStorage("./uploads")
	app.RegisterPlugin(&aemeath.StoragePlugin{Storage: localStore})

	// Rute untuk tes upload file
	app.Handle("POST", "/upload", func(c *aemeath.Ctx) error {
		if err := c.R.ParseMultipartForm(10 << 20); err != nil {
			return aemeath.BadRequest("File terlalu besar atau format tidak valid")
		}

		file, header, err := c.R.FormFile("file")
		if err != nil {
			return aemeath.BadRequest("File tidak ditemukan di form")
		}
		defer file.Close()

		// PERUBAHAN PENTING: Cast ke Interface aemeath.Storage
		storage := c.State["storage"].(aemeath.Storage)

		savedName, err := storage.Save(header.Filename, file)
		if err != nil {
			return aemeath.InternalError("Gagal menyimpan file")
		}

		return c.JSON(200, map[string]string{
			"message":  "File berhasil diunggah!",
			"filename": savedName,
		})
	})

	// 11. FITUR PLUGIN FILE LOGGER
	fileLogger, err := aemeath.NewFileLogger("access.log")
	if err != nil {
		log.Fatal("Gagal membuat logger:", err)
	}
	app.RegisterPlugin(fileLogger)
	defer fileLogger.Close()

	// 12. Dokumentasi & Client
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
