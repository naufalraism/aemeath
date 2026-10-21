# Aemeath CLI

The official Command Line Interface to speed up your development workflow.

## Installation

Since the CLI is still in early development, install it directly via `go install`:

```bash
go install github.com/naufalraism/aemeath-cli@latest
```

## Commands

### `aemeath new`
Scaffolds a new Aemeath project with a ready-to-run `main.go` and `go.mod`.

```bash
aemeath new my-app
```

### `aemeath generate api`
Generates a CRUD handler, complete with Structs and routes.

```bash
aemeath generate api Product
```

### `aemeath dev`
Starts the development server with built-in Hot Reload.

```bash
aemeath dev
```