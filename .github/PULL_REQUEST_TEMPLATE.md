## What does this change?

## Why?

## Checklist

- [ ] `gofmt -l .` prints nothing, `go vet ./...` and `go test -race ./...` pass
- [ ] A test covers the change (a bug fix has a test that fails without it)
- [ ] `CHANGELOG.md` has an entry under *Unreleased* (breaking changes marked)
- [ ] The core package still has no third-party imports
- [ ] If a generator changed: the generated TypeScript passes `tsc --strict` and the Zod schemas agree with the server
