# TypeScript client, Zod schemas and React hooks

```go
app.EnableClient()                   // serves /client.ts, /schemas.ts, /hooks.ts
app.WriteTypeScript("web/src/api")   // or write the files to disk
```

`admin.EnableClient()` on a group serves the same files under the group's prefix, behind its middlewares (see [API docs in production](./auth.md#api-docs-in-production)).

## With the CLI

```bash
aemeath ts --out web/src/api        # build, write the three files, exit
aemeath dev --ts-out web/src/api    # hot reload that also rewrites them after every successful build
```

The CLI builds your program and runs it with `AEMEATH_TS_OUT=<dir>`. `app.Listen` notices the variable, writes the files and returns without starting the server, so `main.go` needs no change. Code that runs before `Listen` does run, but the database plugin notices the variable too: `sqlite.Open`, `postgres.Open` and `mysql.Open` then put GORM in dry-run mode without connecting, so no database server is needed, your SQLite file is not touched and no migration runs. For other startup work that talks to the outside world, check `aemeath.GeneratingTypeScript()` and skip it. Unchanged files are not rewritten, so Vite and webpack do not reload for nothing. If a build fails, `dev` keeps the previous files and the previous server.

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

- **Input**: a field is optional unless it has `validate:"required"` (or is a non-pointer struct containing a required field). An optional field of a JSON body also accepts `null` (`note?: string | null`), because the server reads `null` as "not sent".
- **Output**: a field is required unless it has `json:",omitempty"`.
- Nil slices and maps are `null` in JSON, so output slices/maps are typed `T[] | null`. Use `make([]T, 0)` to send `[]`.
- `oneof` becomes a literal union (`"admin" | "user"`).
- Cookie parameters are not part of the browser client.

## Large integers

A JavaScript number is exact only up to 2^53 (about 9 × 10^15). An `int64` or `uint64` above that, such as a snowflake id, silently changes in the browser. The generated types carry a note on such fields.

On a 64-bit server `int` and `uint` are 64 bits too, and the same limit applies, but they get no note: they usually hold counts, prices and auto-increment ids, which never come near 2^53, and a note on every `int` would soon be ignored. Declare a field `int64` or `uint64` when its values can be that large, so the note shows where it matters.

If a field can hold values that large, send it as a string:

```go
ID uint64 `json:"id,string"`
```

The client, the Zod schemas and OpenAPI then type it as a string.
