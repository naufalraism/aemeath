# Validation

Add a `validate` tag to any input field. Rules are separated by commas.

```go
type CreateUserIn struct {
	Name  string `json:"name"  validate:"required,min=3,max=50"`
	Email string `json:"email" validate:"required,email"`
	Role  string `json:"role"  validate:"oneof=admin user guest"`
	Age   *int   `json:"age"   validate:"required,gte=18,lt=120"`
}
```

| Rule | Applies to | Meaning |
| :--- | :--- | :--- |
| `required` | all | present and non-empty |
| `min=N`, `max=N` | string (characters), slice/map (items), number (value) | inclusive bounds |
| `len=N` | string, slice, map | exactly N |
| `gt`, `gte`, `lt`, `lte` | numbers | comparisons |
| `oneof=a b c` | string, number | one of the values |
| `email`, `url`, `uuid` | string | `email`: `local@domain.tld` (non-ASCII letters allowed; `a@b` is refused). `url`: absolute http/https. `uuid`: 8-4-4-4-12 hex, any version. The Zod schemas and OpenAPI use the same patterns as the server |
| `pattern=REGEX` | string | regular expression (RE2). It takes the rest of the tag, so write it last: `validate:"required,pattern=^\\d{1,3}$"` (or `^[0-9]{1,3}$`). A struct tag is a Go string, so **a backslash must be doubled**: with `\d` the tag cannot be read, and Aemeath refuses to start rather than run without the field's rules. The Zod schemas carry a translation that matches the same strings in JavaScript; `(?m)` and the few other constructs without one are checked by the server only |

## The response

Every failure is reported at once, with status `422`, up to 100 per request:

```json
{
  "error": "validation failed",
  "errors": [
    { "field": "age", "rule": "gte", "message": "must be at least 18" },
    { "field": "address.city", "rule": "required", "message": "is required" }
  ]
}
```

Nested fields are named `address.city`, `items[1].qty` or `extra[home].city`.

Validation stops after `aemeath.MaxValidationErrors` errors (100), and only those are listed, in the order of the fields. Without a limit, a 1 MiB body of empty objects made the server build a 25 MB response. Set it to `0` for no limit, before the server starts. The Go client returns it as `*aemeath.HTTPError` with `Errors`, and the generated TypeScript `ApiError` has an `errors` array.

## Rules to know

- Rules other than `required` only run when a value was provided.
  - In a **JSON body**, a key that is present is provided, even when it is empty: `{"website": ""}` fails `url`, `{"tags": []}` fails `min=1`, `{"kind": 0}` fails `oneof=1 2`. Leave the key out, or send `null`, for "no value".
  - In **forms and query strings**, an empty value counts as not sent, because an HTML form always submits its empty inputs.
  - The generated Zod schemas follow the JSON rule, so the browser and the server agree.
- A scalar (`int`, `bool`, `float`) with `required` must be a pointer, so "not sent" differs from "sent as zero". Aemeath **panics at startup** otherwise, and for unknown or misapplied rules (`min` on a bool, a bad regex, ...).
- For checks only your handler can make: `return aemeath.ValidationError(aemeath.FieldError{Field: "email", Rule: "unique", Message: "is already taken"})`.

## One definition, everywhere

The same tags produce the OpenAPI constraints, the TypeScript types and JSDoc, and the Zod schemas (`schemas.ts`), so a browser form can enforce exactly what the server enforces.
