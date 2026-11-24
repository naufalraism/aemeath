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
	"sync/atomic"
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

	hooksMu       sync.Mutex
	shutdownHooks []func(context.Context) error

	// Set by SetMaxBodyBytes and the others; nil reads the package variable.
	maxBody, maxMultipart, maxRequest *int64
	maxValidation                     *int

	shutdownDelay   time.Duration
	shutdownTimeout time.Duration
	shuttingDown    atomic.Bool
	serving         atomic.Bool // set by the first request; Use panics afterwards

	stopping     chan struct{} // closed when the server starts to shut down
	stoppingOnce sync.Once
}

// Stopping returns a channel that is closed when the server starts to shut
// down (after the delay of SetShutdownDelay). Requests in flight still get
// their time to finish (SetShutdownTimeout), but handlers that would run for ever, such as a
// polling loop or a WebSocket read loop, should return when it closes.
// Streams opened with Ctx.SSE and connections from ws.Upgrade already do: the
// stream's context is cancelled and the WebSocket is closed.
//
// The channel never closes when the app is not run by Listen or Serve (for
// example inside httptest); call Stop yourself then.
func (a *App) Stopping() <-chan struct{} { return a.stopping }

// Stop closes the Stopping channel. Listen and Serve call it when a stop
// signal arrives; call it yourself only when you run the http.Server on your
// own. It is safe to call more than once.
func (a *App) Stop() { a.stoppingOnce.Do(func() { close(a.stopping) }) }

// SetShutdownDelay makes the server keep serving for d after SIGINT/SIGTERM
// while GET /readyz already answers 503, before it stops accepting
// connections. Kubernetes and other load balancers then have time to stop
// sending traffic, so no request is refused during a rolling update. A second
// signal skips the wait. The default is 0 (stop at once).
//
// Keep d below the platform's grace period (30 seconds on Kubernetes), minus
// the time given to in-flight requests (SetShutdownTimeout, 10 s by default).
func (a *App) SetShutdownDelay(d time.Duration) {
	if d < 0 {
		d = 0
	}
	a.shutdownDelay = d
}

// DefaultShutdownTimeout is how long Listen and Serve wait, after a stop
// signal, for requests in flight to finish before closing their connections.
const DefaultShutdownTimeout = 10 * time.Second

// SetShutdownTimeout changes how long requests in flight get to finish after
// SIGINT/SIGTERM (DefaultShutdownTimeout, 10 s). When it runs out, the
// remaining connections are closed: their clients get no response and their
// handlers see c.Context() cancelled. Keep the delay of SetShutdownDelay plus
// this timeout below the platform's grace period (30 s on Kubernetes, 10 s on
// some PaaS), or the process is killed first. d <= 0 restores the default.
func (a *App) SetShutdownTimeout(d time.Duration) { a.shutdownTimeout = d }

// ShuttingDown reports whether the server has received a stop signal. Long
// handlers (streams, polling loops) can use it to finish early.
func (a *App) ShuttingDown() bool { return a.shuttingDown.Load() }

// OnShutdown registers a function that runs when the server stops (after
// in-flight requests have finished), for example to flush a queue or close a
// connection. Hooks run in reverse order of registration. Each hook gets its
// own 10 second limit, so a slow one cannot use up the time of the others,
// and a failing hook is logged without stopping the rest.
func (a *App) OnShutdown(fn func(context.Context) error) {
	a.hooksMu.Lock()
	defer a.hooksMu.Unlock()
	a.shutdownHooks = append(a.shutdownHooks, fn)
}

func (a *App) runShutdownHooks() {
	a.hooksMu.Lock()
	hooks := append([]func(context.Context) error(nil), a.shutdownHooks...)
	a.hooksMu.Unlock()
	if len(hooks) == 0 {
		return
	}
	for i := len(hooks) - 1; i >= 0; i-- {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownHookTimeout)
		err := hooks[i](ctx)
		cancel()
		if err != nil {
			log.Printf("aemeath: shutdown hook failed: %v", err)
		}
	}
}

// shutdownHookTimeout is the time each shutdown hook gets (a variable so
// tests can shorten it).
var shutdownHookTimeout = 10 * time.Second

// New creates a new Aemeath application.
func New() *App { return &App{mux: http.NewServeMux(), stopping: make(chan struct{})} }

// OnError replaces the default error responder. fn receives every error a
// handler or middleware returns, plus 404 and 405 errors; use AsHTTPError to
// get the status code. The default writes {"error": "..."} as JSON.
func (a *App) OnError(fn func(*Ctx, error)) { a.errHandler = fn }

// writeError sends err to the client through the configured error handler.
func (a *App) writeError(c *Ctx, err error) {
	if c.rw.started {
		// The handler already sent (part of) a response; anything written now
		// would be glued onto it. Log the error instead.
		log.Printf("aemeath: %s %s: error after the response was sent: %v", c.R.Method, c.R.URL.EscapedPath(), err)
		return
	}
	if a.errHandler != nil {
		a.errHandler(c, err)
		return
	}
	he := toHTTPError(err)
	writeJSON(c.W, he.Code, he)
}

// Provide makes a service available to every handler through Get[T] and
// MustGet[T]. Call it during startup, before the server runs. For an
// interface, name it explicitly: Provide[storage.Storage](app, store).
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
func (a *App) Use(m ...Middleware) {
	a.mustNotBeServing("App.Use")
	a.mws = append(a.mws, m...)
}

// mustNotBeServing panics when a middleware is added after the first request.
// Each route builds its middleware chain once, on its first request, so a
// later Use would apply to some routes and silently not to others.
func (a *App) mustNotBeServing(what string) {
	if a.serving.Load() {
		panic("aemeath: " + what + " was called after the app started serving requests; set up middlewares and limits during startup")
	}
}

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
		c := newCtx(w, r, a)
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
	if !a.serving.Load() {
		a.serving.Store(true)
	}
	a.limitBody(w, r)
	// "*" is the request target of "OPTIONS *" (a question about the server,
	// not a resource). ServeMux.ServeHTTP answers it 400 before routing;
	// asking mux.Handler directly would skip that and answer 404 or 405.
	star := r.RequestURI == "*"
	var allow string
	var known bool
	if !star {
		h, pattern := a.mux.Handler(r)
		if pattern != "" {
			a.mux.ServeHTTP(w, r)
			return
		}

		// Nothing matched. Let the standard mux tell us whether this is a
		// 404 (unknown path) or a 405 (known path, wrong method).
		probe := &probeWriter{header: http.Header{}, code: http.StatusNotFound}
		h.ServeHTTP(probe, r)
		allow = probe.header.Get("Allow")
		known = probe.code == http.StatusMethodNotAllowed
	}

	// Unmatched requests still pass through the global middlewares, so they
	// are logged, rate limited, and carry CORS and security headers.
	c := newCtx(w, r, a)
	if allow != "" {
		w.Header().Set("Allow", allow)
	}
	wrapped := a.wrap(func(c *Ctx) error {
		switch {
		case star:
			return BadRequest("request target * is not supported")
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

// DefaultMaxHeaderBytes is the header limit NewServer sets (64 KiB): plenty
// for cookies and tokens, and 16 times less memory per connection than
// net/http's default of 1 MiB. Larger headers are answered 431.
const DefaultMaxHeaderBytes = 64 << 10

// NewServer returns an http.Server with safe default timeouts and a header
// limit (DefaultMaxHeaderBytes) that serves the application. Adjust the returned server (for example WriteTimeout for
// streaming responses) and pass it to Serve.
func (a *App) NewServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           a,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    DefaultMaxHeaderBytes,
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

// GeneratingTypeScript reports whether the program was started only to write
// its TypeScript files ("aemeath ts", "aemeath dev --ts-out"). Listen then
// writes the files and exits without serving. Startup code that talks to the
// outside world (connecting to a queue, calling an API, seeding data) can skip
// that work; the database plugin already does (see package db).
func GeneratingTypeScript() bool { return os.Getenv(EnvTypeScriptOut) != "" }

func (a *App) run(srv *http.Server, start func() error) error {
	// "aemeath ts" and "aemeath dev --ts-out" run the program with this
	// variable set: write client.ts, schemas.ts and hooks.ts, then exit without
	// serving. All routes are registered before Listen is called, so they are
	// known by now.
	if GeneratingTypeScript() {
		dir := os.Getenv(EnvTypeScriptOut)
		if err := a.WriteTypeScript(dir); err != nil {
			return fmt.Errorf("aemeath: writing TypeScript to %s: %w", dir, err)
		}
		log.Printf("aemeath: %s is set: wrote the TypeScript files to %q and exited without starting the server", EnvTypeScriptOut, dir)
		return nil
	}
	defer a.runShutdownHooks()
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
		a.shuttingDown.Store(true) // /readyz answers 503 from now on
		if a.shutdownDelay > 0 {
			log.Printf("aemeath: %v received: not ready any more, still serving for %v so load balancers can drain (send it again to stop now)", sig, a.shutdownDelay)
			t := time.NewTimer(a.shutdownDelay)
			select {
			case <-t.C:
			case <-shutdown:
				t.Stop()
			case err := <-serverErrors:
				t.Stop()
				if err != nil && !errors.Is(err, http.ErrServerClosed) {
					return fmt.Errorf("aemeath: server error: %w", err)
				}
				return nil
			}
		}
		log.Printf("aemeath: shutting down gracefully (signal: %v)", sig)
		a.Stop() // streams and WebSockets end now, so they do not hold up the shutdown

		// Give in-flight requests time to finish.
		grace := a.shutdownTimeout
		if grace <= 0 {
			grace = DefaultShutdownTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), grace)
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
