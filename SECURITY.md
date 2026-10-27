# Security Policy

## Supported versions

Aemeath is pre-1.0. Security fixes are made on the latest release only.

## Reporting a vulnerability

**Please do not open a public issue for a security problem.**

Report it privately through GitHub: *Security* tab → *Report a vulnerability* (private vulnerability reporting) on https://github.com/naufalraism/aemeath. Include:

- what you found and which version or commit it affects,
- steps or a minimal program that reproduces it,
- the impact you expect.

You will get an acknowledgement as soon as the maintainer can, and we will keep you informed while a fix is prepared. Once a fix is released you are welcome to be credited in the changelog.

## Scope

In scope: the Aemeath packages in this repository (request binding and validation, middleware, the JWT/WebSocket/static-file/storage code, the OpenAPI and TypeScript generators) and the code the CLI generates.

Out of scope: vulnerabilities in third-party dependencies (report those upstream; tell us if an upgrade is needed), and issues that need an already-compromised server.

## Secure defaults worth knowing

- `jwt.Middleware` rejects an empty secret, non-HMAC algorithms and tokens without `exp`.
- WebSockets accept same-origin browser connections only.
- Static files never list directories; `LocalStorage` strips directory parts from file names but does **not** validate file types: do that yourself.
- `RateLimiter` keys on the connection address; behind a reverse proxy, limit at the proxy.
- `CORS` refuses to combine credentials with the `*` origin.
