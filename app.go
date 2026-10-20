package aemeath

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

// Ctx membungkus request dan response dengan state yang bisa dibagikan antar middleware.
type Ctx struct {
	W     http.ResponseWriter
	R     *http.Request
	State map[string]any
}

// Param mengambil nilai path parameter berdasarkan nama.
func (c *Ctx) Param(name string) string { return c.R.PathValue(name) }

// Query mengambil nilai query string berdasarkan nama.
func (c *Ctx) Query(name string) string { return c.R.URL.Query().Get(name) }

// JSON mengirim respons dalam format JSON beserta HTTP status code.
func (c *Ctx) JSON(code int, v any) error {
	writeJSON(c.W, code, v)
	return nil
}

// HandlerFunc adalah tipe fungsi untuk menangani request di dalam Aemeath.
type HandlerFunc func(*Ctx) error

// Middleware adalah fungsi yang membungkus HandlerFunc untuk memodifikasi request/response.
type Middleware func(HandlerFunc) HandlerFunc

// App adalah struktur utama aplikasi Aemeath.
type App struct {
	mux   *http.ServeMux
	mws   []Middleware
	specs []routeSpec
}

// New membuat instance Aemeath baru.
func New() *App { return &App{mux: http.NewServeMux()} }

// Use mendaftarkan satu atau lebih middleware secara global.
func (a *App) Use(m ...Middleware) { a.mws = append(a.mws, m...) }

// wrap membungkus handler dengan semua middleware yang terdaftar.
func (a *App) wrap(h HandlerFunc) HandlerFunc {
	for i := len(a.mws) - 1; i >= 0; i-- {
		h = a.mws[i](h)
	}
	return h
}

// Handle mendaftarkan route biasa (tanpa binding JSON otomatis).
func (a *App) Handle(method, path string, h HandlerFunc) {
	var once sync.Once
	var wrapped HandlerFunc
	a.mux.HandleFunc(method+" "+path, func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { wrapped = a.wrap(h) })
		c := &Ctx{W: w, R: r, State: make(map[string]any)}
		if err := wrapped(c); err != nil {
			he := toHTTPError(err)
			writeJSON(c.W, he.Code, he)
		}
	})
}

// ServeHTTP memenuhi antarmuka http.Handler dan menangani request masuk.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, pattern := a.mux.Handler(r)
	if pattern == "" {
		if r.Method == http.MethodOptions {
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

// Listen menjalankan server HTTP pada alamat (addr) yang ditentukan.
func (a *App) Listen(addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           a,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return srv.ListenAndServe()
}

// writeJSON adalah helper internal untuk mengirim response JSON.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if v != nil {
		if err := json.NewEncoder(w).Encode(v); err != nil {
			log.Printf("aemeath: gagal encode JSON: %v", err)
		}
	}
}
