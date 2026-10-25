# Getting Started

Get up and running with Aemeath in less than 2 minutes. Aemeath is designed to be ergonomic, type-safe, and blazing fast.

## Prerequisites

Ensure you have **Go 1.22** or newer installed on your machine. You can verify this by running:

```bash
go version
```

## Option 1: Using Aemeath CLI (Recommended)

The fastest way to start a new Aemeath project is by using the official CLI. It scaffolds the project structure, sets up `go.mod`, and runs the dev server with Hot Reload.

```bash
# 1. Install the CLI
go install github.com/naufalraism/aemeath-cli@latest

# 2. Create a new project
aemeath new my-app

# 3. Start the dev server
cd my-app
aemeath dev
```

## Option 2: Manual Installation

If you prefer to set things up manually, create a new directory and initialize a Go module:

```bash
mkdir my-app && cd my-app
go mod init my-app
go get github.com/naufalraism/aemeath
```

## Your First API

Create a file named `main.go` and add the following code. Notice how Aemeath uses Go structs to automatically handle JSON binding and validation.

```go
package main

import (
	"log"
	"github.com/naufalraism/aemeath"
)

// Define your request contract
type HelloIn struct {
	Name string `path:"name"`
}

// Define your response contract
type HelloOut struct {
	Message string `json:"message"`
}

func main() {
	app := aemeath.New()

	// Register a type-safe route
	aemeath.GET(app, "/hello/{name}", func(c *aemeath.Ctx, in HelloIn) (HelloOut, error) {
		return HelloOut{Message: "Hello, " + in.Name}, nil
	})

	// Start the server
	log.Fatal(app.Listen(":3000"))
}
```

Run your server:

```bash
go run main.go
```

Open your browser and navigate to `http://localhost:3000/hello/Budi`. You will see the JSON response: `{"message":"Hello, Budi"}`.

## The Magic: Auto-Generated Docs & Clients

Aemeath's real power lies in what you **don't** have to write. 

Add these two lines right before `app.Listen`:

```go
	// 1. Generate OpenAPI (Swagger) documentation
	app.EnableDocs(aemeath.Info{Title: "My API", Version: "1.0.0"})
	
	// 2. Generate TypeScript Client for the frontend
	app.EnableClient()
```

Restart your server (`Ctrl+C`, then `go run main.go`), and check out:

1. **Interactive API Docs**: Open `http://localhost:3000/docs` in your browser. You get a beautiful Scalar UI generated directly from your Go code.
2. **TypeScript Client**: Open `http://localhost:3000/client.ts`. You will see a fully-typed TypeScript client that the frontend team can download and use immediately. No manual typing required!

## Next Steps

Now that you have a taste of Aemeath, dive deeper into the core concepts:

- [Aemeath CLI Overview](../cli/overview.md)
