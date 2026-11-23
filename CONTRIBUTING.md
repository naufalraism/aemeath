# Contributing to Aemeath

Thanks for helping! This guide keeps changes easy to review.

## Setup

```bash
git clone https://github.com/naufalraism/aemeath
cd aemeath
go test -race ./...
```

You need the Go version in `go.mod`. To check the generated TypeScript you also need Node 22 (see "Changing a generator").

## Ground rules

- **The core package (`github.com/naufalraism/aemeath`) must not import third-party modules.** A feature that needs one belongs in its own sub-package (like `jwt`, `db`, `cache`).
- Every behavior change comes with a test. A bug fix comes with a test that fails without the fix.
- Run `gofmt -l .` (it must print nothing), `go vet ./...` and `go test -race ./...`.
- Add an entry under **Unreleased** in `CHANGELOG.md`, and mark breaking changes clearly.
- Keep comments, messages and docs in English.
- Misusing the API (an invalid `validate` rule, a `required` scalar without a pointer) should `panic` at startup with a clear message, never fail silently at runtime.

## Where things live

| Path | What |
| :--- | :--- |
| `route.go`, `validate.go`, `fields.go` | typed routes, input binding, the validation engine |
| `openapi.go`, `typescript.go`, `zod.go` | the generators; they must stay in agreement with the server's rules |
| `ops.go`, `middleware.go`, `cors.go`, `ratelimit.go`, ... | middleware and operations |
| `jwt/`, `ws/`, `db/`, `cache/`, `mailer/`, `oauth2/` | plugins, one package each |
| `aetest/` | in-process test client |
| `benchmarks/` | comparison with other frameworks (separate module) |

## Changing a generator

The generated TypeScript must compile under `tsc --strict`, and the generated Zod schemas must accept and reject exactly what the server does. When you change `typescript.go` or `zod.go`, run the server of a small example, fetch `/client.ts`, `/schemas.ts` and `/hooks.ts`, and type-check them. CI does exactly that in the "TypeScript generators" job, which you can run locally:

```bash
go run ./examples/tscheck -write examples/tscheck/generated
cd examples/tscheck && npm install && npm run typecheck
go run . &        # then, from examples/tscheck: npm run verify
```

The generated `client.ts`, `schemas.ts`, `hooks.ts` and `openapi.json` of that example are also kept in `examples/tscheck/testdata/golden`, and `go test ./...` fails when they change. After an intended change, regenerate them and commit them with the change, so the review shows what users' frontends will receive:

```bash
go test ./examples/tscheck -update
```

Every exported name (functions and methods with their signatures, types, fields, constants, variables) is listed in `testdata/api.txt`, and `go test .` fails when the list changes. After v1.0 an exported name cannot change without a v2, so a change to it must be on purpose: update the list and commit it with the change, so the review shows it.

```bash
go test -run TestPublicAPI -update-api .
```

## Pull requests

1. Open an issue first for anything larger than a bug fix, so we can agree on the design.
2. Keep a pull request to one change.
3. Fill in the template, including the checklist.

## Security

Do not report vulnerabilities in public issues; see [SECURITY.md](SECURITY.md).

## Code of conduct

Everyone taking part is expected to follow the [code of conduct](CODE_OF_CONDUCT.md).
