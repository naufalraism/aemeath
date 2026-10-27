# Getting Started

## Installation

Use the Go version declared in the project's [`go.mod`](https://github.com/naufalraism/aemeath/blob/main/go.mod) (currently **Go 1.27**).

```bash
go get github.com/naufalraism/aemeath
```

## Quick Start

Create a new file `main.go` and add the following code:

```go
package main

import (
	"log"

	"github.com/naufalraism/aemeath"
)

func main() {
	app := aemeath.New()

	aemeath.GET(app, "/hello", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
		return map[string]string{"message": "Hello Aemeath!"}, nil
	})

	if err := app.Listen(":3000"); err != nil {
		log.Fatal(err)
	}
}
```

Run your server:

```bash
go run main.go
```

Open `http://localhost:3000/hello` in your browser.

## Next steps

- [Routing](/guide/routing): path, query and body binding, validation, groups.
- Add `app.EnableDocs(aemeath.Info{Title: "My API"})` and open `/docs` for interactive API documentation.
- Add `app.EnableClient()` and open `/client.ts` for a generated, fully typed TypeScript client.
