package aemeath

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"sync"
	"syscall"
	"time"
)

// HandlerFunc handles a request inside Aemeath.
type HandlerFunc func(*Ctx) error

// Middleware wraps a HandlerFunc to modify the request or response.
type Middleware func(HandlerFunc) HandlerFunc

// App is the main Aemeath application.
type App struct {
	mux        *http.ServeMux
	mws        []Middleware
	specs      []routeSpec
	errHandler func(*Ctx, error)

	depsMu sync.RWMutex
	deps   map[reflect.Type]any

	readyMu     sync.Mutex
	readyChecks []namedCheck
}

// New creates a new Aemeath application.
func New() *App { return &App{mux: http.NewServeMux()} }

// OnError replaces the default error responder. fn receives every error a
// handler or middleware returns, plus 404 and 405 errors; use AsHTTPError to
// get the status code. The default writes {"error": "..."} as JSON.
func (a *App) OnError(fn func(*Ctx, error)) { a.errHandler = fn }

// writeError sends err to the client through the configured error handler.
func (a *App) writeError(c *Ctx, err error) {
	if a.errHandler != nil {
		a.errHandler(c, err)
		return
	}
	he := toHTTPError(err)
	writeJSON(c.W, he.Code, he)
}

// Provide makes a service available to every handler through Get[T] and
// MustGet[T]. Call it during startup, before the server runs. For an
// interface, name it explicitly: Provide[aemeath.Storage](app, store).
func Provide[T any](a *App, v T) {
	a.depsMu.Lock()
	defer a.depsMu.Unlock()
	if a.deps == nil {
		a.deps = map[reflect.Type]any{}
	}
	a.deps[reflect.TypeFor[T]()] = v
}

// Lookup is like Get, but for use during startup, when there is no request yet
// (for example inside a plugin's Apply).
func Lookup[T any](a *App) (T, bool) {
	var zero T
	a.depsMu.RLock()
	v, ok := a.deps[reflect.TypeFor[T]()]
	a.depsMu.RUnlock()
	if !ok {
		return zero, false
	}
	t, ok := v.(T)
	return t, ok
}

// Get returns the service of type T that was registered with Provide.
func Get[T any](c *Ctx) (T, bool) {
	var zero T
	if c == nil || c.app == nil {
		return zero, false
	}
	c.app.depsMu.RLock()
	v, ok := c.app.deps[reflect.TypeFor[T]()]
	c.app.depsMu.RUnlock()
	if !ok {
		return zero, false
	}
	t, ok := v.(T)
	return t, ok
}

// MustGet is like Get but panics with a clear message when the service is
// missing (Recover turns that into a 500), instead of a bare nil dereference.
func MustGet[T any](c *Ctx) T {
	v, ok := Get[T](c)
	if !ok {
		panic(fmt.Sprintf("aemeath: no %s has been provided; register its plugin or call aemeath.Provide first", reflect.TypeFor[T]()))
	}
	return v
}

// Use registers one or more global middlewares.
//
// Middlewares are resolved lazily on the first request to each route, so
// calling Use after registering routes still works, but only until the first
// request has been served.
func (a *App) Use(m ...Middleware) { a.mws = append(a.mws, m...) }

// wrap wraps a handler with every registered global middleware.
func (a *App) wrap(h HandlerFunc) HandlerFunc {
	for i := len(a.mws) - 1; i >= 0; i-- {
		h = a.mws[i](h)
	}
	return h
}

// Handle registers a plain route (no automatic JSON binding).
func (a *App) Handle(method, path string, h HandlerFunc) {
	var once sync.Once
	var wrapped HandlerFunc
	a.mux.HandleFunc(method+" "+path, func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { wrapped = a.wrap(h) })
		c := &Ctx{W: w, R: r, State: make(map[string]any), app: a}
		if err := wrapped(c); err != nil {
			a.writeError(c, err)
		}
	})
}

// probeWriter captures the status code and headers that the standard
// ServeMux would send for an unmatched request, without writing anything.
type probeWriter struct {
	header http.Header
	code   int
}

func (p *probeWriter) Header() http.Header         { return p.header }
func (p *probeWriter) Write(b []byte) (int, error) { return len(b), nil }
func (p *probeWriter) WriteHeader(code int)        { p.code = code }

// ServeHTTP implements http.Handler.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h, pattern := a.mux.Handler(r)
	if pattern != "" {
		a.mux.ServeHTTP(w, r)
		return
	}

	// Nothing matched. Let the standard mux tell us whether this is a
	// 404 (unknown path) or a 405 (known path, wrong method).
	probe := &probeWriter{header: http.Header{}, code: http.StatusNotFound}
	h.ServeHTTP(probe, r)
	allow := probe.header.Get("Allow")
	known := probe.code == http.StatusMethodNotAllowed

	// Unmatched requests still pass through the global middlewares, so they
	// are logged, rate limited, and carry CORS and security headers.
	c := &Ctx{W: w, R: r, State: make(map[string]any), app: a}
	if allow != "" {
		w.Header().Set("Allow", allow)
	}
	wrapped := a.wrap(func(c *Ctx) error {
		switch {
		case r.Method == http.MethodOptions && known:
			// Plain OPTIONS on a known path (CORS preflights are answered by the CORS middleware).
			c.W.WriteHeader(http.StatusNoContent)
			return nil
		case known:
			return MethodNotAllowed("method not allowed")
		default:
			return NotFound("route not found")
		}
	})
	if err := wrapped(c); err != nil {
		a.writeError(c, err)
	}
}

// NewServer returns an http.Server with safe default timeouts that serves
// the application. Adjust the returned server (for example WriteTimeout for
// streaming responses) and pass it to Serve.
func (a *App) NewServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           a,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

// Listen starts an HTTP server with graceful shutdown on SIGINT/SIGTERM.
func (a *App) Listen(addr string) error {
	return a.Serve(a.NewServer(addr))
}

// ListenTLS starts an HTTPS server with graceful shutdown on SIGINT/SIGTERM.
func (a *App) ListenTLS(addr, certFile, keyFile string) error {
	srv := a.NewServer(addr)
	return a.run(srv, func() error { return srv.ListenAndServeTLS(certFile, keyFile) })
}

// Serve runs a custom http.Server with graceful shutdown on SIGINT/SIGTERM.
// The server's Handler is replaced with the application.
func (a *App) Serve(srv *http.Server) error {
	srv.Handler = a
	return a.run(srv, srv.ListenAndServe)
}

// EnvTypeScriptOut is the environment variable the Aemeath CLI sets to ask an
// application for its TypeScript files (see App.Listen).
const EnvTypeScriptOut = "AEMEATH_TS_OUT"

func (a *App) run(srv *http.Server, start func() error) error {
	// "aemeath ts" and "aemeath dev --ts-out" run the program with this
	// variable set: write client.ts, schemas.ts and hooks.ts, then exit without
	// serving. All routes are registered before Listen is called, so they are
	// known by now.
	if dir := os.Getenv(EnvTypeScriptOut); dir != "" {
		if err := a.WriteTypeScript(dir); err != nil {
			return fmt.Errorf("aemeath: writing TypeScript to %s: %w", dir, err)
		}
		log.Printf("aemeath: %s is set: wrote the TypeScript files to %s and exited without starting the server", EnvTypeScriptOut, dir)
		return nil
	}
	// Capture errors from the server goroutine.
	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("aemeath: server listening on %s", srv.Addr)
		serverErrors <- start()
	}()

	// Catch Ctrl+C or SIGTERM (Docker/Kubernetes).
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(shutdown)

	select {
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("aemeath: server error: %w", err)
		}
		return nil

	case sig := <-shutdown:
		log.Printf("aemeath: shutting down gracefully (signal: %v)", sig)

		// Give in-flight requests 10 seconds to finish.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("aemeath: graceful shutdown failed: %v", err)
			// Force close once the deadline has passed.
			if err := srv.Close(); err != nil {
				return fmt.Errorf("aemeath: failed to close server: %w", err)
			}
			log.Println("aemeath: server closed forcefully")
			return nil
		}
		log.Println("aemeath: server stopped gracefully")
	}

	return nil
}
