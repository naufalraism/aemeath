// Command notes is the app built step by step in docs/guide/tutorial.md:
// sessions, CSRF, validation, a file upload, metrics and tracing, with an
// in-memory store so it runs with no database.
//
//	go run ./examples/notes
package main

import (
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"sync"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/csrf"
	"github.com/naufalraism/aemeath/metrics"
	"github.com/naufalraism/aemeath/session"
)

// Note is what the API stores and returns.
type Note struct {
	ID     int    `json:"id"`
	Owner  string `json:"owner"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Attach string `json:"attachment,omitempty"`
}

type store struct {
	mu    sync.Mutex
	next  int
	notes map[int]Note
	users map[string]string // name -> password (a real app stores a password HASH, see the guide)
}

type credentials struct {
	Name     string `json:"name" validate:"required,min=3,max=20"`
	Password string `json:"password" validate:"required,min=8"`
}

type tokenOut struct {
	Token string `json:"csrf_token"`
}

type meOut struct {
	Name string `json:"name"`
}

type createNote struct {
	Title  string                `form:"title" validate:"required,max=100"`
	Body   string                `form:"body" validate:"max=5000"`
	Attach *multipart.FileHeader `form:"attachment" accept:"image/png,image/jpeg" maxsize:"2MB"`
}

type noteID struct {
	ID int `path:"id"`
}

type empty struct{}

func newApp(insecureCookies bool) *aemeath.App {
	st := &store{notes: map[int]Note{}, users: map[string]string{}}
	reg := metrics.New()

	app := aemeath.New()
	app.Use(reg.Middleware(), aemeath.Recover(), aemeath.SecurityHeaders())
	app.Use(session.Middleware(session.Config{Store: session.NewMemoryStore(), Insecure: insecureCookies}))
	app.Use(csrf.Middleware(csrf.Config{Insecure: insecureCookies}))
	app.EnableHealth()
	app.Handle("GET", "/metrics", reg.Handler()) // keep this internal in production

	login := func(c *aemeath.Ctx, name string) error {
		s := session.From(c)
		if err := s.Renew(c.Context()); err != nil { // new id at login: no session fixation
			return err
		}
		s.Set("user", name)
		return nil
	}
	current := func(c *aemeath.Ctx) (string, error) {
		u := session.From(c).String("user")
		if u == "" {
			return "", aemeath.Unauthorized("log in first")
		}
		return u, nil
	}

	aemeath.GET(app, "/api/csrf", func(c *aemeath.Ctx, _ empty) (tokenOut, error) {
		return tokenOut{Token: csrf.Token(c)}, nil
	}, aemeath.Summary("Get the CSRF token to send as X-CSRF-Token"))

	aemeath.POST(app, "/api/signup", func(c *aemeath.Ctx, in credentials) (meOut, error) {
		st.mu.Lock()
		_, exists := st.users[in.Name]
		if !exists {
			st.users[in.Name] = in.Password
		}
		st.mu.Unlock()
		if exists {
			return meOut{}, aemeath.ValidationError(aemeath.FieldError{Field: "name", Rule: "unique", Message: "is already taken"})
		}
		return meOut{Name: in.Name}, login(c, in.Name)
	}, aemeath.Status(http.StatusCreated))

	aemeath.POST(app, "/api/login", func(c *aemeath.Ctx, in credentials) (meOut, error) {
		st.mu.Lock()
		pw, ok := st.users[in.Name]
		st.mu.Unlock()
		if !ok || pw != in.Password {
			return meOut{}, aemeath.Unauthorized("wrong name or password")
		}
		return meOut{Name: in.Name}, login(c, in.Name)
	})

	aemeath.POST(app, "/api/logout", func(c *aemeath.Ctx, _ empty) (empty, error) {
		session.From(c).Destroy()
		return empty{}, nil
	})

	aemeath.POST(app, "/api/notes", func(c *aemeath.Ctx, in createNote) (Note, error) {
		owner, err := current(c)
		if err != nil {
			return Note{}, err
		}
		n := Note{Owner: owner, Title: in.Title, Body: in.Body}
		if in.Attach != nil {
			n.Attach = fmt.Sprintf("%d bytes", in.Attach.Size) // store the file with aemeath.Storage in a real app
		}
		st.mu.Lock()
		st.next++
		n.ID = st.next
		st.notes[n.ID] = n
		st.mu.Unlock()
		return n, nil
	}, aemeath.Status(http.StatusCreated))

	aemeath.GET(app, "/api/notes/{id}", func(c *aemeath.Ctx, in noteID) (Note, error) {
		owner, err := current(c)
		if err != nil {
			return Note{}, err
		}
		st.mu.Lock()
		n, ok := st.notes[in.ID]
		st.mu.Unlock()
		if !ok || n.Owner != owner { // someone else's note looks exactly like a missing one
			return Note{}, aemeath.NotFound("note not found")
		}
		return n, nil
	})
	return app
}

func main() {
	app := newApp(os.Getenv("DEV") == "1") // DEV=1 allows cookies over plain http://localhost
	if err := app.Listen(":8080"); err != nil {
		log.Fatal(err)
	}
}
