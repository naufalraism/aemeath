# Getting Started

## Installation

Use the Go version declared in the project's [`go.mod`](https://github.com/naufalraism/aemeath/blob/main/go.mod) (currently **Go 1.27**).

```bash
go get github.com/naufalraism/aemeath
```

## Quick Start

Create a new file `main.go` and add the following code:

```go
package main

import (
	"log"

	"github.com/naufalraism/aemeath"
)

func main() {
	app := aemeath.New()

	aemeath.GET(app, "/hello", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
		return map[string]string{"message": "Hello Aemeath!"}, nil
	})

	if err := app.Listen(":3000"); err != nil {
		log.Fatal(err)
	}
}
```

Run your server:

```bash
go run main.go
```

Open `http://localhost:3000/hello` in your browser.

## Configuration and secrets

Aemeath does not read `.env` files and does not decide how you configure your app. Your `main.go` reads environment variables with `os.Getenv`, and you choose how they get there.

During development, a `.env` file plus the small [`godotenv`](https://github.com/joho/godotenv) package is the usual choice:

```bash
go get github.com/joho/godotenv
```

```go
import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load() // loads .env if it exists; real environment variables still win

	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		log.Fatal("JWT_SECRET is not set")
	}
	// ...
}
```

Two rules keep secrets safe:

- Add `.env` to `.gitignore` and never commit it. Commit a `.env.example` with fake values instead.
- In production, set real environment variables through your host (Docker, systemd, your cloud platform) rather than shipping a `.env` file.

## Next steps

- [Routing](/guide/routing): path, query and body binding, validation, groups.
- Add `app.EnableDocs(aemeath.Info{Title: "My API"})` and open `/docs` for interactive API documentation.
- Add `app.EnableClient()` and open `/client.ts` for a generated, fully typed TypeScript client.
