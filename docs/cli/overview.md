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

### `aemeath dev`
Starts the development server with built-in Hot Reload.

```bash
aemeath dev
```

### `aemeath generate api`
Generates a CRUD handler, complete with Structs and routes.

```bash
aemeath generate api Product
```

### `aemeath generate model`
Generates a database struct (GORM) for your entity.

```bash
aemeath generate model Product
```

### `aemeath generate middleware`
Generates a custom middleware template.

```bash
aemeath generate middleware Auth
```

### `aemeath generate test`
Generates a unit test file for your handler.

```bash
aemeath generate test Product
```

### `aemeath generate docker`
Generates a production-ready `Dockerfile` and `docker-compose.yml`.

```bash
aemeath generate docker
```

### `aemeath generate job`
Generates a background job template.

```bash
aemeath generate job SendEmail
```
