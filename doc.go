// Package aemeath is a web framework built on net/http in which each route is
// described by a plain input struct and an output struct. From those structs
// Aemeath binds and validates the request, writes the JSON response, and
// generates the OpenAPI document, a TypeScript client, Zod schemas and
// TanStack Query hooks, all following the rules the server enforces.
//
//	type HelloIn struct {
//		Name string `path:"name"`
//		Lang string `query:"lang" validate:"oneof=en es"`
//	}
//
//	type HelloOut struct {
//		Message string `json:"message"`
//	}
//
//	func main() {
//		app := aemeath.New()
//		app.Use(aemeath.Logger(), aemeath.Recover())
//
//		aemeath.GET(app, "/hello/{name}", func(c *aemeath.Ctx, in HelloIn) (HelloOut, error) {
//			return HelloOut{Message: "Hello, " + in.Name}, nil
//		})
//
//		app.EnableDocs(aemeath.Info{Title: "Hello API", Version: "1.0.0"}) // /docs, /openapi.json
//		app.EnableClient()                                                 // /client.ts, /schemas.ts, /hooks.ts
//		log.Fatal(app.Listen(":3000"))
//	}
//
// # Inputs and validation
//
// Struct tags say where each field comes from (path, query, header, cookie,
// json, form) and what it must satisfy (validate). A request that breaks the
// rules gets a 422 listing every failing field (up to 100); the handler is not
// called.
//
// # Errors
//
// A handler returns an error to answer with it: NotFound, BadRequest and the
// other helpers create an *HTTPError with a status and a message the client
// may see. Any other error answers 500 "internal server error", and its cause
// goes to the app's logger, never to the client.
//
// # Packages
//
// This package has no third-party dependencies. Databases, sessions, CSRF,
// JWT, OpenID Connect, metrics, tracing, background jobs, WebSockets and the
// rest are subpackages, imported only when used.
//
// Guides: https://naufalraism.github.io/aemeath/
package aemeath
