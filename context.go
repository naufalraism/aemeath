package aemeath

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
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

	app      *App
	cleanups []func()
	rw       responseTracker // c.W starts as &rw; lives inside Ctx, so it costs no allocation
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
	writeJSON(c.W, code, v)
	return nil
}

// bodyAllowed reports whether a response with this status may carry a body.
func bodyAllowed(code int) bool {
	return (code < 100 || code >= 200) && code != http.StatusNoContent && code != http.StatusNotModified
}

// writeJSON is an internal helper that writes a JSON response. The value is
// encoded before anything is sent, so a value that cannot be encoded (NaN, a
// channel, a cycle) becomes a 500 instead of a 200 with an empty body.
func writeJSON(w http.ResponseWriter, code int, v any) {
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
		log.Printf("aemeath: failed to encode JSON: %v", err)
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
func (c *Ctx) File(path string) error {
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
	c  *Ctx
	rc *http.ResponseController
}

// SSE starts a Server-Sent Events response. The server's write timeout is
// disabled for this stream, and the stream ends when the handler returns or
// the client disconnects (watch c.Context().Done()).
func (c *Ctx) SSE() (*SSE, error) {
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
	return &SSE{c: c, rc: rc}, nil
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
	if _, err := fmt.Fprintf(s.c.W, ": %s\n\n", text); err != nil {
		return err
	}
	return s.rc.Flush()
}
