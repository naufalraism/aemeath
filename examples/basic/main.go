// Command basic is a tour of Aemeath's features. Several parts need external
// services (Redis, an SMTP server, Google OAuth credentials) and simply
// return errors if those are not available.
// This example is for learning on your own computer. Do not run it on a
// public server: it has demo endpoints (such as /send-email) and falls back
// to a development JWT secret that is visible in this repository.
package main

import (
	"context"
	"log"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
	"golang.org/x/oauth2"
	githuboauth "golang.org/x/oauth2/github"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/cache"
	"github.com/naufalraism/aemeath/db"
	"github.com/naufalraism/aemeath/db/sqlite"
	"github.com/naufalraism/aemeath/examples/api"
	"github.com/naufalraism/aemeath/jwt"
	"github.com/naufalraism/aemeath/mailer"
	aeoauth "github.com/naufalraism/aemeath/oauth2"
	"github.com/naufalraism/aemeath/oidc"
	"github.com/naufalraism/aemeath/session"
	"github.com/naufalraism/aemeath/storage"
	"github.com/naufalraism/aemeath/ws"
)

// User is the example database model.
type User struct {
	ID    uint   `gorm:"primaryKey" json:"id"`
	Name  string `json:"name"`
	Email string `gorm:"unique" json:"email"`
}

// devSecret is only used when JWT_SECRET is not set. Never deploy with it.
const devSecret = "dev-only-secret-change-me-0123456789"

func main() {
	// Load the .env file if present.
	if err := godotenv.Load(); err != nil {
		log.Println("warning: .env file not found, using defaults")
	}

	app := aemeath.New()

	// 1. Global middleware and security.
	app.Use(
		aemeath.SecurityHeaders(),
		aemeath.RequestID(),
		aemeath.MaxBodySize(2<<20), // 2 MiB
		aemeath.Recover(),
		aemeath.Logger(),
		aemeath.CORS(),
		aemeath.Gzip(),
	)

	// Liveness (/healthz) and readiness (/readyz) for load balancers; the
	// database and cache plugins add their own readiness checks.
	app.EnableHealth()

	// 2. Serve the landing page from the "website" folder at the root URL.
	app.Static("/", "./website")

	// 3. Default routes (the shared contract lives in the api package).
	aemeath.Register(app, api.Hello, func(c *aemeath.Ctx, in api.HelloIn) (api.HelloOut, error) {
		greeting := "Hello"
		if in.Lang == "es" {
			greeting = "Hola"
		}
		return api.HelloOut{Message: greeting + ", " + in.Name}, nil
	}, aemeath.Summary("Say hello"), aemeath.OperationID("hello"))

	aemeath.Register(app, api.CreateUser, func(c *aemeath.Ctx, in api.CreateUserIn) (api.User, error) {
		return api.User{ID: 1, Name: in.Name, Email: in.Email}, nil
	}, aemeath.Summary("Create a user"), aemeath.Status(201))

	// 4. Route group (admin) with its own middleware.
	adminGroup := app.Group("/admin", func(next aemeath.HandlerFunc) aemeath.HandlerFunc {
		return func(c *aemeath.Ctx) error {
			log.Println("entering the admin area")
			return next(c)
		}
	})
	aemeath.GET(adminGroup, "/dashboard", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
		return map[string]string{"status": "Welcome to the Admin Dashboard"}, nil
	})

	// 5. Database (GORM + SQLite).
	dbPlugin, err := sqlite.Open("aemeath.db", db.WithModels(&User{}))
	if err != nil {
		log.Fatal(err)
	}
	if err := app.RegisterPlugin(dbPlugin); err != nil {
		log.Fatal(err)
	}

	// 6. JWT auth and rate limiting.
	// Read the secret from the environment; fall back to a dev-only value.
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		if os.Getenv("APP_ENV") == "production" {
			log.Fatal("JWT_SECRET must be set when APP_ENV=production")
		}
		log.Println("warning: JWT_SECRET is not set, using an insecure development secret")
		jwtSecret = devSecret
	}

	secretGroup := app.Group("/api/secret")
	// Rate limit first, so unauthenticated requests are limited too.
	secretGroup.Use(
		aemeath.RateLimiter(30, time.Minute),
		jwt.Middleware(jwt.Config{SecretKey: jwtSecret}),
	)
	// RequireAuth is the safety net: a route here answers 401 if the JWT middleware above is ever removed.
	secretGroup.Options(aemeath.Tags("secret"), aemeath.RequireAuth())

	aemeath.GET(secretGroup, "/profile", func(c *aemeath.Ctx, _ struct{}) (map[string]any, error) {
		return map[string]any{
			"message": "Hello! You are signed in.",
			"user_id": jwt.UserID(c),
		}, nil
	})

	aemeath.POST(secretGroup, "/users", func(c *aemeath.Ctx, in api.CreateUserIn) (User, error) {
		user := User{Name: in.Name, Email: in.Email}
		if err := db.From(c).Create(&user).Error; err != nil {
			return user, aemeath.Conflict("could not save the user, the email may already be in use")
		}
		return user, nil
	}, aemeath.Status(201))

	aemeath.GET(secretGroup, "/users", func(c *aemeath.Ctx, _ struct{}) ([]User, error) {
		users := []User{}
		if err := db.From(c).Find(&users).Error; err != nil {
			return nil, aemeath.InternalError("could not load users")
		}
		return users, nil
	})

	// 7. WebSocket echo (same-origin by default; ws.Config{TrustedOrigins: ...} allows others).
	app.Handle("GET", "/ws", func(c *aemeath.Ctx) error {
		conn, err := ws.Upgrade(c)
		if err != nil {
			return nil // Upgrade has already written the error response
		}
		defer func() { _ = conn.CloseNow() }()
		for {
			kind, message, err := conn.Read(c.Context())
			if err != nil {
				return nil // the client left, or the server is stopping
			}
			if err := conn.Write(c.Context(), kind, message); err != nil {
				return nil
			}
		}
	})

	// 8. Mailer plugin (try it with a local SMTP catcher such as MailHog on :1025).
	smtp := mailer.New(mailer.Config{
		Host: "localhost", Port: 1025, Username: "user", Password: "pass",
		From: "Admin <admin@aemeath.dev>",
	})
	if err := app.RegisterPlugin(smtp); err != nil {
		log.Fatal(err)
	}
	aemeath.GET(app, "/send-email", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
		if err := mailer.From(c).Send("customer@example.com", "Hello from Aemeath!", "<h1>Congratulations!</h1>"); err != nil {
			log.Println("failed to send email:", err)
			return nil, aemeath.NewError(http.StatusBadGateway, "failed to send email")
		}
		return map[string]string{"status": "email sent"}, nil
	})

	// 9. Cache plugin (Redis).
	redisCache := cache.New(cache.Config{Addr: "localhost:6379"})
	if err := app.RegisterPlugin(redisCache); err != nil {
		log.Fatal(err)
	}
	defer func() { _ = redisCache.Close() }()
	aemeath.GET(app, "/cache-test", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
		redisClient := cache.From(c)
		ctx := c.Context()
		key := "hit_counter"

		val, err := redisClient.Get(ctx, key).Result()
		if err == redis.Nil {
			if err := redisClient.Set(ctx, key, "1", 10*time.Minute).Err(); err != nil {
				return nil, aemeath.InternalError("failed to write to Redis")
			}
			return map[string]string{"message": "Cache created!", "value": "1"}, nil
		} else if err != nil {
			return nil, aemeath.InternalError("failed to reach Redis")
		}
		return map[string]string{"message": "Served from cache", "value": val}, nil
	})

	// 10. File storage (upload). Provided under the interface type, so a
	// handler reads it as storage.Storage, whatever the backend.
	aemeath.Provide[storage.Storage](app, storage.NewLocal("./uploads"))
	// The accept tag checks the file's bytes (not its name), maxsize its
	// size; a file that fails either is refused with 422 before the handler.
	type UploadIn struct {
		File *multipart.FileHeader `form:"file" accept:"image/png,image/jpeg,image/gif,application/pdf" maxsize:"10MB" validate:"required"`
	}
	aemeath.POST(app, "/upload", func(c *aemeath.Ctx, in UploadIn) (map[string]string, error) {
		file, err := in.File.Open()
		if err != nil {
			return nil, aemeath.BadRequest("could not read the file")
		}
		defer func() { _ = file.Close() }()

		savedName, err := aemeath.MustGet[storage.Storage](c).Save(c.Context(), in.File.Filename, file)
		if err != nil {
			return nil, aemeath.InternalError("failed to save the file")
		}
		return map[string]string{"message": "File uploaded!", "filename": savedName}, nil
	})

	// 11. Access log in a file, as JSON lines; closed when the server stops.
	accessLog, err := os.OpenFile("access.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		log.Fatal("failed to open the access log:", err)
	}
	app.Use(aemeath.SlogLogger(slog.New(slog.NewJSONHandler(accessLog, nil))))
	app.OnShutdown(func(context.Context) error { return accessLog.Close() })

	// 12. Social login. Both flows keep their state in the server-side
	// session and check it, so the routes need session.Middleware. Cookies
	// are Secure unless APP_ENV=development (plain http on localhost).
	auth := app.Group("/auth", session.Middleware(session.Config{
		Store:    session.NewMemoryStore(),
		Insecure: os.Getenv("APP_ENV") == "development",
	}))

	// Google speaks OpenID Connect: use package oidc, which also verifies the
	// ID token. Discover fetches Google's configuration, so it only runs when
	// the credentials are set (fill in the .env file to try it).
	if os.Getenv("GOOGLE_CLIENT_ID") != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		google, err := oidc.Discover(ctx, oidc.Config{
			Issuer:       "https://accounts.google.com",
			ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
			ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
			RedirectURL:  os.Getenv("GOOGLE_REDIRECT_URL"), // e.g. http://localhost:3000/auth/google/callback
		})
		cancel()
		if err != nil {
			log.Fatal("google login: ", err)
		}
		auth.Handle("GET", "/google/login", func(c *aemeath.Ctx) error { return google.Begin(c) })
		auth.Handle("GET", "/google/callback", func(c *aemeath.Ctx) error {
			id, err := google.Complete(c)
			if err != nil {
				return err
			}
			// Complete has already given the session a new id.
			session.From(c).Set("user", id.Issuer+"|"+id.Subject) // issuer + subject, never the e-mail
			return c.JSON(200, map[string]any{"message": "Login successful!", "email": id.Email, "name": id.Name})
		})
	}

	// GitHub login is plain OAuth2 (no OpenID Connect): package oauth2.
	github := aeoauth.New(aeoauth.Config{
		Name: "github",
		OAuth2: &oauth2.Config{
			ClientID:     os.Getenv("GITHUB_CLIENT_ID"),
			ClientSecret: os.Getenv("GITHUB_CLIENT_SECRET"),
			RedirectURL:  os.Getenv("GITHUB_REDIRECT_URL"), // e.g. http://localhost:3000/auth/github/callback
			Scopes:       []string{"read:user"},
			Endpoint:     githuboauth.Endpoint,
		},
		UserInfoURL: "https://api.github.com/user",
	})
	auth.Handle("GET", "/github/login", func(c *aemeath.Ctx) error { return github.Begin(c) })
	auth.Handle("GET", "/github/callback", func(c *aemeath.Ctx) error {
		login, err := github.Complete(c)
		if err != nil {
			return err
		}
		session.From(c).Set("github_id", login.User["id"]) // GitHub's numeric id: logins can be renamed; Complete renewed the id
		return c.JSON(200, map[string]any{"message": "Login successful!", "login": login.User["login"]})
	})

	// 13. Documentation and generated client.
	app.EnableDocs(aemeath.Info{Title: "Aemeath Demo", Version: "0.1.0"})
	app.EnableClient()

	if len(os.Args) > 1 && os.Args[1] == "gen" {
		if err := app.WriteTypeScript("ts-client"); err != nil {
			log.Fatal(err)
		}
		log.Println("ts-client/client.ts, schemas.ts and hooks.ts written")
		return
	}

	// Read PORT from the environment (.env), default to 3000.
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	log.Printf("running at http://localhost:%s (docs: /docs, client: /client.ts)\n", port)
	if err := app.Listen(":" + port); err != nil {
		log.Fatal(err)
	}
}
