package main

import (
	"encoding/json"
	"net/http"
	"net/mail"
	"runtime"
	"strings"

	"github.com/naufalraism/aemeath"
)

// The three servers serve exactly the same routes:
//   GET  /hello  -> 200 {"message":"Hello"}
//   POST /users  -> 201 {"id":1,"name":...,"email":...}  (name 3-50 characters, valid email)
//   GET  /__mem  -> process memory (for the report, not measured)

type HelloOut struct {
	Message string `json:"message"`
}

type UserIn struct {
	Name  string `json:"name" validate:"required,min=3,max=50" binding:"required,min=3,max=50"`
	Email string `json:"email" validate:"required,email" binding:"required,email"`
}

type UserOut struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func memHandler(w http.ResponseWriter, _ *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]uint64{"sys": m.Sys, "heap_inuse": m.HeapInuse, "goroutines": uint64(runtime.NumGoroutine())})
}

func netHTTPServer() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(HelloOut{Message: "Hello"})
	})
	mux.HandleFunc("POST /users", func(w http.ResponseWriter, r *http.Request) {
		var in UserIn
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
		n := len([]rune(in.Name))
		_, mailErr := mail.ParseAddress(in.Email)
		if n < 3 || n > 50 || in.Email == "" || mailErr != nil || strings.ContainsAny(in.Email, " <>") {
			http.Error(w, `{"error":"validation failed"}`, http.StatusUnprocessableEntity)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(UserOut{ID: 1, Name: in.Name, Email: in.Email})
	})
	mux.HandleFunc("GET /__mem", memHandler)
	return mux
}

func aemeathServer() http.Handler {
	app := aemeath.New()
	aemeath.GET(app, "/hello", func(c *aemeath.Ctx, _ struct{}) (HelloOut, error) {
		return HelloOut{Message: "Hello"}, nil
	})
	aemeath.POST(app, "/users", func(c *aemeath.Ctx, in UserIn) (UserOut, error) {
		return UserOut{ID: 1, Name: in.Name, Email: in.Email}, nil
	}, aemeath.Status(http.StatusCreated))
	app.Handle("GET", "/__mem", func(c *aemeath.Ctx) error { memHandler(c.W, c.R); return nil })
	return app
}
