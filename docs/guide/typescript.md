# TypeScript client, Zod schemas and React hooks

```go
app.EnableClient()                   // serves /client.ts, /schemas.ts, /hooks.ts
app.WriteTypeScript("web/src/api")   // or write the files to disk
```

## With the CLI

```bash
aemeath ts --out web/src/api        # build, write the three files, exit
aemeath dev --ts-out web/src/api    # hot reload that also rewrites them after every successful build
```

The CLI builds your program and runs it with `AEMEATH_TS_OUT=<dir>`. `app.Listen` notices the variable, writes the files and returns without starting the server, so `main.go` needs no change (but code that runs before `Listen`, such as opening a database, does run). Unchanged files are not rewritten, so Vite and webpack do not reload for nothing. If a build fails, `dev` keeps the previous files and the previous server.

Do not write `log.Fatal(app.Listen(...))`: when `Listen` returns `nil`, `log.Fatal(nil)` exits with status 1. Use `if err := app.Listen(addr); err != nil { log.Fatal(err) }`.

## client.ts

```ts
import { createClient, ApiError } from "./client";
const api = createClient({ baseUrl: "http://localhost:3000" });

try {
  await api.postUsers({ body: { name: "Ana", email: "ana@example.com" } });
} catch (e) {
  if (e instanceof ApiError && e.status === 422) console.log(e.errors); // [{ field, rule, message }]
}
```

## schemas.ts (needs `zod`)

One schema per type, plus one per operation input: `postUsersBodySchema`, `getUsersQuerySchema`, `getUsersByIdPathSchema`, `...HeadersSchema`.

```ts
const result = postUsersBodySchema.safeParse(formValues);
```

## hooks.ts (needs `@tanstack/react-query` v5)

```ts
const { useGetUsers, usePostUsers } = createHooks(api);

const users = useGetUsers({ query: { limit: 20 } });   // useQuery
const create = usePostUsers();                         // useMutation
create.mutate({ body: { name: "Ana", email: "ana@example.com" } });
```

## Typing rules

- **Input**: a field is optional unless it has `validate:"required"` (or is a non-pointer struct containing a required field).
- **Output**: a field is required unless it has `json:",omitempty"`.
- Nil slices and maps are `null` in JSON, so output slices/maps are typed `T[] | null`. Use `make([]T, 0)` to send `[]`.
- `oneof` becomes a literal union (`"admin" | "user"`).
- Cookie parameters are not part of the browser client.
