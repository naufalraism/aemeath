# How Aemeath compares

*Last checked October 2026 from each project's public documentation. Projects change quickly: verify before you decide, and tell us if something here is out of date.*

## The short version

- Pick **Huma** if you want the most mature, router-agnostic way to build an OpenAPI-first Go API, with RFC 9457 errors and content negotiation (JSON, CBOR).
- Pick **Fuego** if you like a NestJS-style API on `net/http`, `go-playground/validator`'s large rule set, or an adapter for an existing Gin/Echo server.
- Pick **Gin / Echo / Fiber / chi** if you want a router and nothing else, or the largest ecosystem of middleware.
- Pick **Aemeath** if your frontend is TypeScript and you want the validation rules, types, schemas and hooks to come from one Go struct, with a core that depends on nothing but the standard library.

## Feature table

| | Aemeath | Huma | Fuego | Gin / Echo / chi / Fiber |
| :--- | :--- | :--- | :--- | :--- |
| Typed handlers (input/output structs) | yes | yes | yes | no (manual binding) |
| OpenAPI from code | yes (3.0.3) | yes (3.x, JSON Schema) | yes (3.x) | no (third-party) |
| Router | `net/http` `ServeMux` only | bring your own (chi, Gin, Echo, ...) | `net/http`; adapters for Gin/Echo | their own |
| Validation | built-in rules (`min`, `max`, `oneof`, `email`, `pattern`, ...); 422 with per-field errors | built-in via struct tags and JSON Schema | `go-playground/validator` | `go-playground/validator` (Gin) or manual |
| Typed TypeScript client | built-in | via the OpenAPI document and a generator | via the OpenAPI document and a generator | via a generator, if you document your API |
| Zod schemas, React Query hooks | built-in, same rules as the server | not built-in as far as we know | not built-in as far as we know | no |
| Errors | `{"error", "errors"[]}` (replaceable) | RFC 9457 `problem+json` | JSON | your choice |
| Content negotiation | JSON only | JSON, CBOR | JSON, XML, ... | your choice |
| In-process test client | `aetest` | `humatest` | via `httptest` | via `httptest` |
| Third-party deps in the core | none | few | `go-playground/validator` and others | varies |
| Maturity | pre-1.0, single maintainer | mature, widely used | established | very mature |

## Where Aemeath is weaker (today)

- **Maturity and ecosystem.** Huma and the router frameworks have far more users, adapters and battle-testing.
- **One router.** Aemeath is `net/http` `ServeMux`; you cannot plug it into an existing chi/Gin app the way Huma and Fuego can.
- **OpenAPI 3.0.3 only**, no content negotiation, no per-operation request size limits.
- **A smaller validation rule set** than `go-playground/validator`, by design: every rule has to work in Go, OpenAPI and Zod.
- No OpenTelemetry/Prometheus integration, no job runner, and no typed SSE in the TypeScript client yet.

## Where Aemeath aims to win

1. **One source of truth for validation.** The same `validate` tags drive the server, OpenAPI, the TypeScript types and the Zod schemas; a parity test checks that the server and the generated Zod schemas agree.
2. **A TypeScript-first workflow.** Client, schemas and hooks are generated, typed against your real errors (`ApiError.errors`), and kept honest about `null` and optional fields.
3. **A dependency-free core**, with plugins you opt into one import at a time.
4. **Testing without a network**, through the same typed endpoints your clients use.
