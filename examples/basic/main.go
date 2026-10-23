package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/examples/api"
	"github.com/redis/go-redis/v9"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"gorm.io/gorm"
)

func main() {
	// Muat file .env jika ada
	if err := godotenv.Load(); err != nil {
		log.Println("⚠️  Peringatan: File .env tidak ditemukan. Menggunakan nilai default.")
	}

	app := aemeath.New()

	// 1. Middleware Global & Keamanan
	app.Use(
		aemeath.SecurityHeaders(),
		aemeath.MaxBodySize(2<<20), // Maksimal 2MB
		aemeath.Recover(),
		aemeath.Logger(),
		aemeath.CORS(),
	)

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
	if err := app.RegisterPlugin(dbPlugin); err != nil {
		log.Fatal(err)
	}
	// 6. Fitur JWT Auth & Rate Limiter
	// Ambil secret dari .env. Jika kosong, pakai fallback (hanya untuk dev).
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		jwtSecret = "fallback-rahasia-dev-jangan-dipakai-di-production"
	}

	jwtAuth := aemeath.JWTMiddleware(aemeath.JWTConfig{
		SecretKey: jwtSecret, // <-- Diubah di sini
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
		defer func() { _ = conn.Close() }()
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
	if err := app.RegisterPlugin(mailer); err != nil {
		log.Fatal(err)
	}
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
	if err := app.RegisterPlugin(cache); err != nil {
		log.Fatal(err)
	}
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

	// 10. FITUR PLUGIN STORAGE (UPLOAD FILE)
	localStore := aemeath.NewLocalStorage("./uploads")
	if err := app.RegisterPlugin(&aemeath.StoragePlugin{Storage: localStore}); err != nil {
		log.Fatal(err)
	}

	app.Handle("POST", "/upload", func(c *aemeath.Ctx) error {
		if err := c.R.ParseMultipartForm(10 << 20); err != nil {
			return aemeath.BadRequest("File terlalu besar atau format tidak valid")
		}

		file, header, err := c.R.FormFile("file")
		if err != nil {
			return aemeath.BadRequest("File tidak ditemukan di form")
		}
		defer func() { _ = file.Close() }()

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
	if err := app.RegisterPlugin(fileLogger); err != nil {
		log.Fatal(err)
	}

	defer func() { _ = fileLogger.Close() }()

	// 13. FITUR PLUGIN OAUTH2 (GOOGLE LOGIN)
	// CATATAN: Rute ini butuh Client ID & Secret asli dari Google Cloud.
	// Tanpa itu, akan muncul Error 400. Isi file .env untuk mengetesnya.
	googleConfig := &oauth2.Config{
		ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		RedirectURL:  os.Getenv("OAUTH_REDIRECT_URL"),
		Scopes:       []string{"https://www.googleapis.com/auth/userinfo.email", "https://www.googleapis.com/auth/userinfo.profile"},
		Endpoint:     google.Endpoint, // <--- Perlu import "golang.org/x/oauth2/google"
	}

	googleOAuth := aemeath.NewOAuth2(
		"google",
		googleConfig,
		"https://www.googleapis.com/oauth2/v2/userinfo",
	)
	app.RegisterPlugin(googleOAuth)

	// Rute 1: Redirect ke Google
	aemeath.GET(app, "/auth/google/login", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
		oauth := c.State["oauth"].(*aemeath.OAuthPlugin)
		url := oauth.GetAuthURL("state-rahasia-123") // Di production, gunakan random string
		return map[string]string{"url": url}, nil
	})

	// Rute 2: Callback dari Google
	aemeath.GET(app, "/auth/google/callback", func(c *aemeath.Ctx, _ struct{}) (map[string]any, error) {
		code := c.R.URL.Query().Get("code")
		if code == "" {
			return nil, aemeath.BadRequest("Code tidak ditemukan")
		}

		oauth := c.State["oauth"].(*aemeath.OAuthPlugin)
		token, err := oauth.Exchange(c.R.Context(), code)
		if err != nil {
			return nil, aemeath.NewError(401, "Gagal menukar token")
		}

		userInfo, err := oauth.GetUserInfo(c.R.Context(), token)
		if err != nil {
			return nil, aemeath.InternalError("Gagal mengambil data user")
		}

		return map[string]any{
			"message": "Login berhasil!",
			"user":    userInfo,
		}, nil
	})

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

	// Ambil PORT dari .env (jika ada), kalau tidak ada pakai 3000
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	log.Printf("jalan di http://localhost:%s (docs: /docs, client: /client.ts)\n", port)
	log.Fatal(app.Listen(":" + port))
}
