// Command basic is a tour of Aemeath's features. Several parts need external
// services (Redis, an SMTP server, Google OAuth credentials) and simply
// return errors if those are not available.
package main

import (
	"crypto/subtle"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/cache"
	"github.com/naufalraism/aemeath/db"
	"github.com/naufalraism/aemeath/examples/api"
	"github.com/naufalraism/aemeath/jwt"
	"github.com/naufalraism/aemeath/mailer"
	aeoauth "github.com/naufalraism/aemeath/oauth2"
	"github.com/naufalraism/aemeath/ws"
)

// User is the example database model.
type User struct {
	ID    uint   `gorm:"primaryKey" json:"id"`
	Name  string `json:"name"`
	Email string `gorm:"unique" json:"email"`
}

// devSecret is only used when JWT_SECRET is not set. Never deploy with it.
const devSecret = "dev-only-secret-change-me"

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
	aemeath.GroupGET(adminGroup, "/dashboard", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
		return map[string]string{"status": "Welcome to the Admin Dashboard"}, nil
	})

	// 5. Database (GORM + SQLite).
	dbPlugin, err := db.Open("aemeath.db", &User{})
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
		log.Println("warning: JWT_SECRET is not set, using an insecure development secret")
		jwtSecret = devSecret
	}

	secretGroup := app.Group("/api/secret")
	// Rate limit first, so unauthenticated requests are limited too.
	secretGroup.Use(
		aemeath.RateLimiter(30, time.Minute),
		jwt.Middleware(jwt.Config{SecretKey: jwtSecret}),
	)
	// Document the group as protected in /docs (the middleware above enforces it).
	secretGroup.Options(aemeath.Tags("secret"), aemeath.RequireAuth())

	aemeath.GroupGET(secretGroup, "/profile", func(c *aemeath.Ctx, _ struct{}) (map[string]any, error) {
		return map[string]any{
			"message": "Hello! You are signed in.",
			"user_id": jwt.UserID(c),
		}, nil
	})

	aemeath.GroupPOST(secretGroup, "/users", func(c *aemeath.Ctx, in api.CreateUserIn) (User, error) {
		user := User{Name: in.Name, Email: in.Email}
		if err := db.From(c).Create(&user).Error; err != nil {
			return user, aemeath.Conflict("could not save the user, the email may already be in use")
		}
		return user, nil
	}, aemeath.Status(201))

	aemeath.GroupGET(secretGroup, "/users", func(c *aemeath.Ctx, _ struct{}) ([]User, error) {
		users := []User{}
		if err := db.From(c).Find(&users).Error; err != nil {
			return nil, aemeath.InternalError("could not load users")
		}
		return users, nil
	})

	// 7. WebSocket (same-origin by default; see aemeath.WebSocketUpgrader).
	app.Handle("GET", "/ws", func(c *aemeath.Ctx) error {
		conn, err := ws.Upgrade(c)
		if err != nil {
			return nil // the upgrader has already written the error response
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

	// 8. Mailer plugin (try it with a local SMTP catcher such as MailHog on :1025).
	smtp := mailer.New(
		"localhost", 1025, "user", "pass", "Admin <admin@aemeath.dev>",
	)
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
	redisCache := cache.New("localhost:6379", "", 0)
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

	// 10. Storage plugin (file upload).
	localStore := aemeath.NewLocalStorage("./uploads")
	if err := app.RegisterPlugin(&aemeath.StoragePlugin{Storage: localStore}); err != nil {
		log.Fatal(err)
	}
	app.Handle("POST", "/upload", func(c *aemeath.Ctx) error {
		if err := c.R.ParseMultipartForm(10 << 20); err != nil {
			return aemeath.BadRequest("file too large or invalid form")
		}

		file, header, err := c.R.FormFile("file")
		if err != nil {
			return aemeath.BadRequest("no file found in the form")
		}
		defer func() { _ = file.Close() }()

		if !localStore.IsAllowedFile(header.Filename, []string{".png", ".jpg", ".jpeg", ".gif", ".pdf"}) {
			return aemeath.UnsupportedMediaType("only png, jpg, gif and pdf files are allowed")
		}

		savedName, err := aemeath.MustGet[aemeath.Storage](c).Save(header.Filename, file)
		if err != nil {
			return aemeath.InternalError("failed to save the file")
		}

		return c.JSON(http.StatusOK, map[string]string{
			"message":  "File uploaded!",
			"filename": savedName,
		})
	})

	// 11. File logger plugin.
	fileLogger, err := aemeath.NewFileLogger("access.log")
	if err != nil {
		log.Fatal("failed to create the file logger:", err)
	}
	if err := app.RegisterPlugin(fileLogger); err != nil {
		log.Fatal(err)
	}
	defer func() { _ = fileLogger.Close() }()

	// 12. OAuth2 plugin (Google login).
	// NOTE: these routes need a real Client ID and Secret from Google Cloud;
	// without them Google answers with a 400. Fill in the .env file to try it.
	googleConfig := &oauth2.Config{
		ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		RedirectURL:  os.Getenv("OAUTH_REDIRECT_URL"),
		Scopes:       []string{"https://www.googleapis.com/auth/userinfo.email", "https://www.googleapis.com/auth/userinfo.profile"},
		Endpoint:     google.Endpoint,
	}
	googleOAuth := aeoauth.New(
		"google",
		googleConfig,
		"https://www.googleapis.com/oauth2/v2/userinfo",
	)
	if err := app.RegisterPlugin(googleOAuth); err != nil {
		log.Fatal(err)
	}

	// Step 1: return the Google login URL. A random state value is stored in
	// a short-lived cookie and checked in the callback to prevent CSRF.
	aemeath.GET(app, "/auth/google/login", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
		oauth, ok := aeoauth.From(c, "google")
		if !ok {
			return nil, aemeath.InternalError("google oauth is not configured")
		}
		state, err := aeoauth.GenerateState()
		if err != nil {
			return nil, aemeath.InternalError("could not start the login")
		}
		http.SetCookie(c.W, &http.Cookie{
			Name:     "oauth_state",
			Value:    state,
			Path:     "/auth/google",
			MaxAge:   600,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		return map[string]string{"url": oauth.AuthURL(state)}, nil
	})

	// Step 2: handle the callback from Google.
	aemeath.GET(app, "/auth/google/callback", func(c *aemeath.Ctx, _ struct{}) (map[string]any, error) {
		cookie, err := c.R.Cookie("oauth_state")
		got := c.Query("state")
		if err != nil || got == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(got)) != 1 {
			return nil, aemeath.BadRequest("invalid state")
		}
		code := c.Query("code")
		if code == "" {
			return nil, aemeath.BadRequest("code not found")
		}

		oauth, ok := aeoauth.From(c, "google")
		if !ok {
			return nil, aemeath.InternalError("google oauth is not configured")
		}
		token, err := oauth.Exchange(c.Context(), code)
		if err != nil {
			return nil, aemeath.Unauthorized("failed to exchange the token")
		}

		userInfo, err := oauth.UserInfo(c.Context(), token)
		if err != nil {
			return nil, aemeath.InternalError("failed to fetch the user profile")
		}

		return map[string]any{
			"message": "Login successful!",
			"user":    userInfo,
		}, nil
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
