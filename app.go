package aemeath

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

type Ctx struct {
	W     http.ResponseWriter
	R     *http.Request
	State map[string]any
}

func (c *Ctx) Param(name string) string { return c.R.PathValue(name) }
func (c *Ctx) Query(name string) string { return c.R.URL.Query().Get(name) }

func (c *Ctx) JSON(code int, v any) error {
	writeJSON(c.W, code, v)
	return nil
}

type HandlerFunc func(*Ctx) error
type Middleware func(HandlerFunc) HandlerFunc

type App struct {
	mux   *http.ServeMux
	mws   []Middleware
	specs []routeSpec
}

func New() *App { return &App{mux: http.NewServeMux()} }

func (a *App) Use(m ...Middleware) { a.mws = append(a.mws, m...) }

func (a *App) wrap(h HandlerFunc) HandlerFunc {
	for i := len(a.mws) - 1; i >= 0; i-- {
		h = a.mws[i](h)
	}
	return h
}

func (a *App) Handle(method, path string, h HandlerFunc) {
	var once sync.Once
	var wrapped HandlerFunc
	a.mux.HandleFunc(method+" "+path, func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { wrapped = a.wrap(h) })
		// INI BAGIAN YANG DIPERBAIKI:
		c := &Ctx{W: w, R: r, State: make(map[string]any)}
		if err := wrapped(c); err != nil {
			he := toHTTPError(err)
			writeJSON(c.W, he.Code, he)
		}
	})
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, pattern := a.mux.Handler(r)
	if pattern == "" {
		if r.Method == http.MethodOptions {
			// INI JUGA DIPERBAIKI:
			c := &Ctx{W: w, R: r, State: make(map[string]any)}
			h := a.wrap(func(c *Ctx) error { return nil })
			if err := h(c); err != nil {
				he := toHTTPError(err)
				writeJSON(w, he.Code, he)
			}
			return
		}
		writeJSON(w, http.StatusNotFound, NewError(http.StatusNotFound, "rute tidak ditemukan"))
		return
	}
	a.mux.ServeHTTP(w, r)
}

func (a *App) Listen(addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           a,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return srv.ListenAndServe()
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if v != nil {
		if err := json.NewEncoder(w).Encode(v); err != nil {
			log.Printf("aemeath: gagal encode JSON: %v", err)
		}
	}
}
