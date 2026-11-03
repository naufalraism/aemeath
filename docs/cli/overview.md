---
title: Aemeath CLI
---
# Aemeath CLI

The official command line interface: it creates projects, generates code and runs a hot-reload development server.

## Installation

```bash
go install github.com/naufalraism/aemeath-cli/cmd/aemeath@latest
```

This installs a command named `aemeath`. Make sure `$(go env GOPATH)/bin` is in your `PATH`. Without Go installed, download a binary for Windows, macOS or Linux from the [Releases page](https://github.com/naufalraism/aemeath-cli/releases).

## Commands

### `aemeath new`

```bash
aemeath new my-app                       # a minimal API
aemeath new my-app --template db         # + SQLite and a Task CRUD resource
aemeath new my-app --template fullstack  # + a React + TypeScript app in web/
```

The fullstack template generates the typed client, the Zod schemas and the React Query hooks into `web/src/api`, so `npm run dev` works right away.

### `aemeath generate`

```bash
aemeath generate api Product            # CRUD handlers + tests, registered in main.go
aemeath generate api Product --db       # backed by GORM and SQLite
aemeath generate api Product --prefix /api
aemeath generate job SendEmail          # a background job; registers the job runner
aemeath generate model|middleware|test|docker
```

`generate` registers what it creates in `main.go` right after the `// aemeath:plugins` and `// aemeath:handlers` comments. It edits `main.go` only when it parses and only by inserting lines, and otherwise leaves it alone and prints what to add. It never overwrites a file without `--force`, and it writes nothing at all if one of the files already exists.

### `aemeath ts`

```bash
aemeath ts --out web/src/api
```

### `aemeath dev`

```bash
aemeath dev                              # hot reload
aemeath dev --ts-out web/src/api         # also rewrites the TypeScript files after every successful build
```
