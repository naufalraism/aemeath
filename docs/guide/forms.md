# Forms and file uploads

Routes bind JSON by default. Add a `form` tag and the route takes an HTML form instead, either `application/x-www-form-urlencoded` or `multipart/form-data`.

```go
type ProfileIn struct {
	Name   string                `form:"name" validate:"required,max=50"`
	Tags   []string              `form:"tags" validate:"max=5"`          // repeated field: tags=a&tags=b
	Age    *int                  `form:"age" validate:"min=13"`
	Avatar *multipart.FileHeader `form:"avatar" accept:"image/png,image/jpeg" maxsize:"2MB" validate:"required"`
	Docs   []*multipart.FileHeader `form:"docs" accept:"application/pdf" maxsize:"10MB"`
}

aemeath.POST(app, "/profile", func(c *aemeath.Ctx, in ProfileIn) (Profile, error) {
	f, err := in.Avatar.Open()
	if err != nil {
		return Profile{}, err
	}
	defer f.Close()
	// copy f to storage, e.g. aemeath.NewLocalStorage("uploads").Save(name, f)
	return Profile{Name: in.Name}, nil
})
```

`validate` rules work as usual, and errors name the form field: `{"field":"avatar","rule":"accept",...}`. You can still mix in `path:`, `query:`, `header:` and `cookie:` fields.

## Upload rules

| Tag | Meaning |
| :--- | :--- |
| `accept:"image/png,image/jpeg"` | Allowed types; wildcards like `text/*` work. |
| `maxsize:"2MB"` | Per file; accepts bytes, `KB`, `MB`, `GB`. |
| `validate:"required"` | The file must be sent. |

- **The type is detected from the file's bytes**, not from the name or the `Content-Type` the client claims. A script renamed to `.png` is rejected with `accept`.
- A `*multipart.FileHeader` field takes exactly one file (a second one is a 422); use `[]*multipart.FileHeader` for several.
- The whole body is limited: `aemeath.DefaultMaxMultipartBytes` (32 MiB) for multipart and `DefaultMaxBodyBytes` (1 MiB) for urlencoded forms. Larger requests get 413. Change them before starting the server.
- Uploads beyond 8 MiB spill to temporary files. They are **deleted when the handler returns**, so copy what you need inside the handler.
- Never use the client's file name as a path. `aemeath.LocalStorage` generates safe names and stores files mode `0600`.
- Form values come from the request **body only**, never from the URL query, so `?user=attacker` cannot fill a form field.

A file field needs `multipart/form-data`; a route with only text fields accepts both encodings. A `form` field on a `GET` or `DELETE` route, together with another source tag on the same field, or `maxsize`/`accept` on a non-file field all panic at startup.

## Generated clients

- **OpenAPI** documents the body as `multipart/form-data` (files are `type: string, format: binary`) or `application/x-www-form-urlencoded`.
- **TypeScript** gets a `form` section, and the generated `request` sends a real `FormData`:

```ts
await api.postProfile({ form: { name: "Ann", tags: ["a", "b"], avatar: fileInput.files![0] } });
```

- **Zod** gets a `…FormSchema` with `z.instanceof(Blob)` for files.
