# Getting Started

## Installation

Ensure you are using **Go 1.22** or newer.

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

	log.Fatal(app.Listen(":3000"))
}
```

Run your server:

```bash
go run main.go
```

Open `http://localhost:3000/hello` in your browser.