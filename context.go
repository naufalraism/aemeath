package aemeath

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Ctx wraps the request and response together with per-request state that
// middlewares and handlers can share.
type Ctx struct {
	W     http.ResponseWriter
	R     *http.Request
	State map[string]any

	app       *App
	cleanups  []func()
	authed    bool            // set by MarkAuthenticated; checked by routes with RequireAuth
	requestID string          // set by the RequestID middleware
	rw        responseTracker // c.W starts as &rw; lives inside Ctx, so it costs no allocation
}

// newCtx creates the Ctx of a request. Its writer records whether the
// response has started, so an error returned afterwards is not appended to it.
func newCtx(w http.ResponseWriter, r *http.Request, a *App) *Ctx {
	c := &Ctx{R: r, State: make(map[string]any), app: a}
	c.rw.ResponseWriter = w
	c.W = &c.rw
	return c
}

// responseTracker records whether the status line has been sent.
type responseTracker struct {
	http.ResponseWriter
	started bool
}

func (t *responseTracker) WriteHeader(code int) {
	if code >= 200 || code == http.StatusSwitchingProtocols {
		t.started = true
	}
	t.ResponseWriter.WriteHeader(code)
}

func (t *responseTracker) Write(b []byte) (int, error) {
	t.started = true
	return t.ResponseWriter.Write(b)
}

// ReadFrom keeps the sendfile fast path of http.ServeFile and c.File.
func (t *responseTracker) ReadFrom(r io.Reader) (int64, error) {
	t.started = true
	if rf, ok := t.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(r)
	}
	return io.Copy(t.ResponseWriter, r)
}

func (t *responseTracker) Flush() {
	t.started = true
	_ = http.NewResponseController(t.ResponseWriter).Flush()
}

func (t *responseTracker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	t.started = true
	return http.NewResponseController(t.ResponseWriter).Hijack()
}

func (t *responseTracker) Unwrap() http.ResponseWriter { return t.ResponseWriter }

// Stopping returns a channel that is closed when the server starts to shut
// down; see App.Stopping. Long-running handlers should return when it closes.
func (c *Ctx) Stopping() <-chan struct{} { return c.app.stopping }

// MarkAuthenticated records that this request comes from an authenticated
// caller. Authentication middlewares call it once they have verified the
// caller (jwt.Middleware does); call it from your own middleware when you
// authenticate another way, for example with a session:
//
//	if session.From(c).String("user") != "" {
//		c.MarkAuthenticated()
//	}
//
// Routes registered with RequireAuth answer 401 unless it was called.
func (c *Ctx) MarkAuthenticated() { c.authed = true }

// Authenticated reports whether MarkAuthenticated was called for this request.
func (c *Ctx) Authenticated() bool { return c.authed }

// Handled is the output type of a typed route that writes the response itself
// (a redirect, a file, a stream). Aemeath then sends nothing after the handler returns.
//
//	aemeath.GET(app, "/old", func(c *aemeath.Ctx, _ struct{}) (aemeath.Handled, error) {
//		return aemeath.Handled{}, c.Redirect(http.StatusMovedPermanently, "/new")
//	})
type Handled struct{}

// Context returns the request's context.
func (c *Ctx) Context() context.Context { return c.R.Context() }

// Param returns a path parameter by name.
func (c *Ctx) Param(name string) string { return c.R.PathValue(name) }

// Query returns a query-string value by name.
func (c *Ctx) Query(name string) string { return c.R.URL.Query().Get(name) }

// JSON sends a JSON response with the given HTTP status code.
func (c *Ctx) JSON(code int, v any) error {
	writeJSON(c, c.W, code, v)
	return nil
}

// bodyAllowed reports whether a response with this status may carry a body.
func bodyAllowed(code int) bool {
	return (code < 100 || code >= 200) && code != http.StatusNoContent && code != http.StatusNotModified
}

// writeJSON is an internal helper that writes a JSON response. The value is
// encoded before anything is sent, so a value that cannot be encoded (NaN, a
// channel, a cycle) becomes a 500 instead of a 200 with an empty body.
func writeJSON(c *Ctx, w http.ResponseWriter, code int, v any) {
	if !bodyAllowed(code) {
		w.WriteHeader(code)
		return
	}
	if v == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		return
	}
	buf := jsonBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer func() {
		if buf.Cap() <= 64<<10 { // do not keep huge buffers alive
			jsonBufPool.Put(buf)
		}
	}()
	if err := json.NewEncoder(buf).Encode(v); err != nil {
		c.Logger().Error("aemeath: encoding the JSON response failed", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"internal server error"}`+"\n")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(buf.Bytes())
}

var jsonBufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// SetCookie adds a Set-Cookie header to the response. It sends the cookie
// exactly as given: set Secure and HttpOnly yourself for anything sensitive,
// such as a session or login cookie.
func (c *Ctx) SetCookie(ck *http.Cookie) { http.SetCookie(c.W, ck) }

// Redirect sends an HTTP redirect. Use it from a route returning Handled.
func (c *Ctx) Redirect(code int, url string) error {
	http.Redirect(c.W, c.R, url, code)
	return nil
}

// File serves a file from disk (with Range and conditional request support).
// Use it from a route returning Handled. Never pass a path built from user input.
// Like Static, it sends X-Content-Type-Options: nosniff and serves a file
// whose extension has no known type as application/octet-stream, never as a
// type guessed from the content.
func (c *Ctx) File(path string) error {
	c.W.Header().Set("X-Content-Type-Options", "nosniff")
	if _, set := c.W.Header()["Content-Type"]; !set && !knownType(filepath.Ext(path)) {
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			c.W.Header().Set("Content-Type", "application/octet-stream")
		}
	}
	http.ServeFile(c.W, c.R, path)
	return nil
}

// Text sends a plain text response.
func (c *Ctx) Text(code int, s string) error {
	c.W.Header().Set("Content-Type", "text/plain; charset=utf-8")
	c.W.WriteHeader(code)
	_, err := fmt.Fprint(c.W, s)
	return err
}

// SSE is an open Server-Sent Events stream.
type SSE struct {
	c            *Ctx
	rc           *http.ResponseController
	writeTimeout time.Duration
}

// DefaultSSEWriteTimeout is how long one Send or Comment may take before the
// stream is given up: a client that stops reading (but keeps the connection
// open) would otherwise block the handler in Send forever.
const DefaultSSEWriteTimeout = 10 * time.Second

// SSE starts a Server-Sent Events response. The server's write timeout is
// replaced by a per-event one (DefaultSSEWriteTimeout, see SetWriteTimeout),
// so the stream may stay open for as long as it is read, and the stream ends
// when the handler returns.
// Watch c.Context().Done(): it fires when the client disconnects and when
// the server starts to shut down.
func (c *Ctx) SSE() (*SSE, error) {
	// A stream has no natural end, so its context is also cancelled when the
	// server starts to shut down; otherwise one connected client would make
	// every shutdown wait for the full grace period.
	ctx, cancel := context.WithCancel(c.R.Context())
	c.R = c.R.WithContext(ctx)
	go func() {
		select {
		case <-c.app.stopping:
			cancel()
		case <-ctx.Done(): // the request ended
		}
	}()
	rc := http.NewResponseController(c.W)
	_ = rc.SetWriteDeadline(time.Time{})
	h := c.W.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // keeps nginx from buffering the stream
	c.W.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		return nil, fmt.Errorf("aemeath: streaming is not supported by this ResponseWriter: %w", err)
	}
	return &SSE{c: c, rc: rc, writeTimeout: DefaultSSEWriteTimeout}, nil
}

// SetWriteTimeout sets how long each Send or Comment may take to reach the
// client; when it runs out, the call returns an error and the connection is
// closed. Zero means no limit.
func (s *SSE) SetWriteTimeout(d time.Duration) { s.writeTimeout = d }

// arm starts the clock for one event. ResponseWriters without deadlines (such
// as httptest.ResponseRecorder) simply ignore it.
func (s *SSE) arm() {
	if s.writeTimeout > 0 {
		_ = s.rc.SetWriteDeadline(time.Now().Add(s.writeTimeout))
	}
}

// Send writes one event. data is encoded as JSON; event may be empty.
func (s *SSE) Send(event string, data any) error {
	if strings.ContainsAny(event, "\r\n") {
		return fmt.Errorf("aemeath: SSE event name must not contain line breaks")
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	s.arm()
	if event != "" {
		if _, err := fmt.Fprintf(s.c.W, "event: %s\n", event); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(s.c.W, "data: %s\n\n", b); err != nil {
		return err
	}
	return s.rc.Flush()
}

// Comment writes a comment line, a handy keep-alive (": ping").
func (s *SSE) Comment(text string) error {
	text = strings.NewReplacer("\r", " ", "\n", " ").Replace(text)
	s.arm()
	if _, err := fmt.Fprintf(s.c.W, ": %s\n\n", text); err != nil {
		return err
	}
	return s.rc.Flush()
}
