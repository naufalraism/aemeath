# Routing

## Typed routes

A typed route takes an input struct and returns an output value. Aemeath binds and validates the input, and serializes the output as JSON.

```go
type GetUserIn struct {
	ID int `path:"id"`
}

type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

aemeath.GET(app, "/users/{id}", func(c *aemeath.Ctx, in GetUserIn) (User, error) {
	return User{ID: in.ID, Name: "Aemeath"}, nil
})
```

Route paths use the Go 1.22 syntax: `/users/{id}`, not `/users/:id`.

## Where input comes from

| Tag | Source | Methods |
| :--- | :--- | :--- |
| `path:"name"` | URL path parameter | all |
| `query:"name"` | query string (slices use repeated keys: `?tag=a&tag=b`) | all |
| `header:"X-Name"` | request header | all |
| `cookie:"name"` | request cookie | all |
| `json:"name"` | JSON request body | `POST`, `PUT`, `PATCH` |

Types implementing `encoding.TextUnmarshaler` (`time.Time`, UUIDs, ...) can be used as parameters. Embedded structs are flattened, so a shared `Pagination` struct can carry `query` tags.

A `json`-tagged field on a `GET` or `DELETE` route is never bound, because those requests have no body. Use `query:"..."` instead.

## Errors and status codes

```go
aemeath.POST(app, "/users", handler, aemeath.Status(201))

// inside a handler
return User{}, aemeath.NotFound("user not found")
```

Errors are sent as `{"error": "message"}`. Any error that is not an `*aemeath.HTTPError` becomes a generic `500`; its cause is logged once through the app's logger (see [Logging](/guide/observability#logging)), with the request id, method and path, and never sent to the client. The exception is an error that wraps `context.Canceled`, which a handler gets (for example from a database call) when the client went away before the response was ready: it is recorded as `499` (`aemeath.StatusClientClosedRequest`), is not logged as an internal error, and is counted as a 4xx by the Logger, metrics and tracing. `context.DeadlineExceeded` is a server-side timeout and stays a `500`. Validation failures are `422`, see [Validation](/guide/validation).

## Groups

```go
admin := app.Group("/admin", authMiddleware)
admin.Options(aemeath.Tags("admin"), aemeath.RequireAuth()) // 401 unless authMiddleware authenticated the caller

aemeath.GET(admin, "/dashboard", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
	return map[string]string{"status": "ok"}, nil
})
```

`GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `Route` and `Register` take a group as well as the app. Inside a group the path is relative to its prefix (`/dashboard` above serves `/admin/dashboard`), and the group's middlewares and options apply. The older `GroupGET` family does the same and is deprecated.

Register a group's middlewares and options before creating sub-groups; a sub-group copies them when it is created.
