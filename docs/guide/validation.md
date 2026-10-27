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
| `email`, `url`, `uuid` | string | formats |
| `pattern=REGEX` | string | regular expression (no commas) |

## The response

Every failure is reported at once, with status `422`:

```json
{
  "error": "validation failed",
  "errors": [
    { "field": "age", "rule": "gte", "message": "must be at least 18" },
    { "field": "address.city", "rule": "required", "message": "is required" }
  ]
}
```

Nested fields are named `address.city`, `items[1].qty` or `extra[home].city`. The Go client returns it as `*aemeath.HTTPError` with `Errors`, and the generated TypeScript `ApiError` has an `errors` array.

## Rules to know

- Rules other than `required` only run when a value was provided.
- A scalar (`int`, `bool`, `float`) with `required` must be a pointer, so "not sent" differs from "sent as zero". Aemeath **panics at startup** otherwise, and for unknown or misapplied rules (`min` on a bool, a bad regex, ...).
- For checks only your handler can make: `return aemeath.ValidationError(aemeath.FieldError{Field: "email", Rule: "unique", Message: "is already taken"})`.

## One definition, everywhere

The same tags produce the OpenAPI constraints, the TypeScript types and JSDoc, and the Zod schemas (`schemas.ts`), so a browser form can enforce exactly what the server enforces.
