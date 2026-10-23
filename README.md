# Aemeath 🚀

[![Go](https://github.com/naufalraism/aemeath/actions/workflows/go.yml/badge.svg)](https://github.com/naufalraism/aemeath/actions/workflows/go.yml) 
[![golangci-lint](https://github.com/naufalraism/aemeath/actions/workflows/golangci-lint.yml/badge.svg)](https://github.com/naufalraism/aemeath/actions/workflows/golangci-lint.yml) 
[![Go Reference](https://pkg.go.dev/badge/github.com/naufalraism/aemeath.svg)](https://pkg.go.dev/github.com/naufalraism/aemeath) 
[![GitHub release](https://img.shields.io/github/v/release/naufalraism/aemeath?sort=semver&display_name=tag&cacheSeconds=3600)](https://github.com/naufalraism/aemeath/releases) 
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**The Modern Standard for Go APIs.**

Aemeath is a next-generation Go web framework designed for exceptional Developer Experience (DX). It combines Go's blazing-fast performance with end-to-end type safety, automated OpenAPI docs, and seamless TypeScript client generation. All built on top of Go's standard `net/http` (Go 1.22+).

## ✨ Features

- **Type-Safe Routing**: Define request/response structs once. Validation and binding are automatic.
- **Auto OpenAPI**: Beautiful Scalar API documentation generated automatically from your code.
- **Client Generators**: Generate fully-typed TypeScript & Go clients directly from your backend.
- **Route Groups**: Group routes with prefixes and specific middlewares.
- **Built-in Middleware**: CORS, Logger, and Recover (Anti-Panic).
- **Static File Server**: Serve HTML/CSS/JS easily.
- **Blazing Fast**: Built entirely on top of Go's standard `net/http`.

---

## 📊 Performance

Aemeath is built on Go's standard `net/http`, delivering top-tier performance without sacrificing Developer Experience. 

*Benchmarked on Intel i7-10750H using Go 1.27 (Higher is better for iterations, lower is better for ns/op).*

| Framework | Time (ns/op) | Memory (B/op) | Allocations |
| :--- | :--- | :--- | :--- |
| `net/http` (Baseline) | 1,246 | 1,040 B | 10 |
| **Aemeath** | **2,238** | **1,120 B** | **13** |
| Gin | 2,521 | 1,434 B | 16 |
| Fiber (via test adapter)| 32,595 | 6,337 B | 32 |

*Aemeath is notably faster and more memory-efficient than Gin in this test, while staying incredibly close to the raw `net/http` baseline.*

## 📦 Installation

Ensure you are using **Go 1.22** or newer.

```bash
go get github.com/naufalraism/aemeath
```

---

## ⚡ Aemeath CLI

Don't write boilerplate. Use the official CLI to scaffold your project, generate CRUD APIs, and start the dev server with Hot Reload.

```bash
# Create a new project
aemeath new my-project

# Generate a CRUD handler (creates handlers/product.go)
aemeath generate api Product

# Start dev server with Hot Reload
aemeath dev
```
[View Aemeath CLI Repository](https://github.com/naufalraism/aemeath-cli)

---

## 🚀 Quick Start

```go
package main

import (
	"log"
	"github.com/naufalraism/aemeath"
)

type HelloIn struct {
	Name string `path:"name"`
	Lang string `query:"lang"`
}
type HelloOut struct {
	Message string `json:"message"`
}

func main() {
	app := aemeath.New()

	// 1. Add Global Middleware
	app.Use(aemeath.Logger(), aemeath.Recover(), aemeath.CORS())

	// 2. Register Route (Type-Safe)
	aemeath.GET(app, "/hello/{name}", func(c *aemeath.Ctx, in HelloIn) (HelloOut, error) {
		greeting := "Halo"
		if in.Lang == "en" {
			greeting = "Hello"
		}
		return HelloOut{Message: greeting + ", " + in.Name}, nil
	}, aemeath.Summary("Greet User"))

	// 3. Start Server
	log.Println("Server running at http://localhost:3000")
	log.Fatal(app.Listen(":3000"))
}
```

---

## 🛡️ Validation & Pointers (Important!)

Aemeath automatically validates input. For **scalar fields** (`int`, `bool`, `float`) marked as `required`, you **must** use pointers (`*int`, `*bool`). This allows Aemeath to differentiate between "not sent" and "sent as zero (0)".

```go
type CreateUserIn struct {
	// Strings are safe without pointers
	Name  string `json:"name" validate:"required"`
	Email string `json:"email" validate:"required"`
	
	// Integers MUST use pointers if required
	Age   *int   `json:"age" validate:"required"`
}
```
*Note: If you use a non-pointer scalar with `required`, Aemeath will **panic at startup** to prevent silent validation bugs in production.*

### Nested Validation
`validate:"required"` works **recursively** for nested structs:
```go
type Address struct {
    City string `json:"city" validate:"required"`
}
type CreateUser struct {
    Name    string  `json:"name" validate:"required"`
    Address Address `json:"address"` // City will be validated too
}
```

### Body Limits
JSON body size is limited by `aemeath.DefaultMaxBodyBytes` (default **1 MiB**).
```go
aemeath.DefaultMaxBodyBytes = 10 << 20 // 10 MiB
```
Requests with a body must send the `Content-Type: application/json` header.

---

## 🗂️ Route Groups & Middleware

Group routes and add specific "guards" (middlewares) for that group.

```go
// Admin Middleware
authAdmin := func(next aemeath.HandlerFunc) aemeath.HandlerFunc {
	return func(c *aemeath.Ctx) error {
		// Check admin token here
		return next(c)
	}
}

// Create Group
admin := app.Group("/admin", authAdmin)

// Register route to Group
aemeath.GroupGET(admin, "/dashboard", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
	return map[string]string{"status": "Welcome Admin"}, nil
})
```

---

## ⚡ Client Generators (TypeScript & Go)

This is Aemeath's killer feature. You never have to manually rewrite API types on the frontend.

### 1. TypeScript Client
Enable the generator with `app.EnableClient()`. Then access `http://localhost:3000/client.ts`:
```ts
import { createClient } from "./client";
const api = createClient({ baseUrl: "http://localhost:3000" });

// Full Type-Safe & Autocomplete!
const res = await api.hello({ path: { name: "Budi" }, query: { lang: "id" } });
```

**TS Requiredness Rules:**
- **Input** (path, query, body): Fields are required **only if** they have `validate:"required"`.
- **Output**: Fields are required **unless** they have the `json:",omitempty"` tag.
- If a struct is used as both input and output, the generator creates two separate interfaces — `FooIn` and `FooOut`.

### 2. Go Client (No Codegen)
Define the contract once, use it on both server and client.

```go
// Define contract
var Hello = aemeath.NewEndpoint[HelloIn, HelloOut]("GET", "/hello/{name}")

// On Server:
aemeath.Register(app, Hello, handler)

// On Client (default 30s timeout):
client := aemeath.NewClient("http://localhost:3000")
out, err := Hello.Call(context.Background(), client, HelloIn{Name: "Budi"})
```
Errors >= 400 are returned as `*aemeath.HTTPError`.

---

## 📖 Auto OpenAPI (Swagger)

Enable documentation with:
```go
app.EnableDocs(aemeath.Info{
	Title:   "Aemeath API",
	Version: "1.0.0",
})
```
Open `http://localhost:3000/docs` to see the Scalar API Reference interface.

---

## 📂 Static File Server

Serve static files (HTML/CSS/Images).
```go
// Serves the "./public" folder at "/public" URL
app.Static("/public", "./public")
```

---

## 🧪 Testing

Aemeath comes fully tested.
```bash
go test ./...
```

## 📝 Technical Notes
- **Route paths** use Go 1.22 syntax: `/hello/{name}`, not `/hello/:name`.
- **Body in `DELETE`** requests is not yet supported (only `POST`, `PUT`, `PATCH`).
- **OpenAPI schemas** are currently inline (no `$ref` yet).

## 📝 License
MIT License.