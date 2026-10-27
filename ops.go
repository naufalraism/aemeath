package aemeath

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------- request id

// HeaderRequestID is the header RequestID reads and writes.
const HeaderRequestID = "X-Request-Id"

// StateRequestID is the Ctx.State key holding the request id.
const StateRequestID = "request_id"

var requestIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// RequestID gives every request an id: the incoming X-Request-Id header when
// it is a safe value (1-64 characters of letters, digits, '.', '_' and '-'),
// otherwise a new random one. The id is echoed in the response header, stored
// in Ctx.State, and printed by Logger and SlogLogger.
func RequestID() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) error {
			id := c.R.Header.Get(HeaderRequestID)
			if !requestIDRe.MatchString(id) {
				id = newRequestID()
			}
			c.State[StateRequestID] = id
			c.W.Header().Set(HeaderRequestID, id)
			return next(c)
		}
	}
}

// RequestID returns the id set by the RequestID middleware, or "".
func (c *Ctx) RequestID() string {
	id, _ := c.State[StateRequestID].(string)
	return id
}

// ---------------------------------------------------------------- slog

// SlogLogger logs every request as one structured record (method, path,
// status, duration, remote address and request id). 5xx are logged at Error
// level, 4xx at Warn, everything else at Info. A nil logger uses slog.Default().
func SlogLogger(l *slog.Logger) Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) (err error) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: c.W, status: http.StatusOK}
			c.W = sw
			logger := l
			if logger == nil {
				logger = slog.Default()
			}
			defer func() {
				attrs := []any{
					"method", c.R.Method,
					"path", c.R.URL.Path,
					"duration_ms", float64(time.Since(start).Microseconds()) / 1000,
					"remote", c.R.RemoteAddr,
				}
				if id := c.RequestID(); id != "" {
					attrs = append(attrs, "request_id", id)
				}
				if r := recover(); r != nil {
					logger.Error("request panicked", append(attrs, "status", http.StatusInternalServerError, "panic", fmt.Sprint(r))...)
					panic(r)
				}
				status := sw.status
				if err != nil {
					status = AsHTTPError(err).Code
				}
				attrs = append(attrs, "status", status)
				switch {
				case status >= 500:
					logger.Error("request", attrs...)
				case status >= 400:
					logger.Warn("request", attrs...)
				default:
					logger.Info("request", attrs...)
				}
			}()
			return next(c)
		}
	}
}

// ---------------------------------------------------------------- timeout

// Timeout gives each request a deadline through its context. Handlers and
// database calls that honor c.Context() stop when it expires, and the client
// receives 504. A handler that ignores the context cannot be interrupted, and
// a response that has already been started cannot be replaced.
func Timeout(d time.Duration) Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) error {
			orig := c.R
			ctx, cancel := context.WithTimeout(orig.Context(), d)
			defer cancel()
			c.R = orig.WithContext(ctx)
			defer func() { c.R = orig }()

			err := next(c)
			if err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)) {
				var he *HTTPError
				if !errors.As(err, &he) {
					return NewError(http.StatusGatewayTimeout, "request timed out")
				}
			}
			return err
		}
	}
}

// ---------------------------------------------------------------- health

// HealthCheck reports whether a dependency is ready (nil error) or not.
type HealthCheck func(ctx context.Context) error

type namedCheck struct {
	name string
	fn   HealthCheck
}

// AddReadyCheck registers a dependency check reported by GET /readyz
// (see EnableHealth), for example a database ping.
func (a *App) AddReadyCheck(name string, fn HealthCheck) {
	a.readyMu.Lock()
	defer a.readyMu.Unlock()
	a.readyChecks = append(a.readyChecks, namedCheck{name, fn})
}

// EnableHealth registers two endpoints for load balancers and Kubernetes:
//
//	GET /healthz   liveness: 200 as long as the process can answer
//	GET /readyz    readiness: 200 when every check from AddReadyCheck passes
//	               (each runs with a 5 second limit), otherwise 503
//
// The checks' error details are logged, not sent to the client.
func (a *App) EnableHealth() {
	a.Handle("GET", "/healthz", func(c *Ctx) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})
	a.Handle("GET", "/readyz", func(c *Ctx) error {
		a.readyMu.Lock()
		checks := append([]namedCheck(nil), a.readyChecks...)
		a.readyMu.Unlock()

		ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
		defer cancel()
		results := make(map[string]string, len(checks))
		status, code := "ok", http.StatusOK
		for _, ch := range checks {
			if err := ch.fn(ctx); err != nil {
				log.Printf("aemeath: readiness check %q failed: %v", ch.name, err)
				results[ch.name] = "failed"
				status, code = "unavailable", http.StatusServiceUnavailable
				continue
			}
			results[ch.name] = "ok"
		}
		return c.JSON(code, map[string]any{"status": status, "checks": results})
	})
}

// ---------------------------------------------------------------- gzip

var gzipPool = sync.Pool{New: func() any { return gzip.NewWriter(nil) }}

func compressible(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	switch {
	case mt == "text/event-stream":
		return false
	case strings.HasPrefix(mt, "text/"),
		strings.HasSuffix(mt, "+json"), strings.HasSuffix(mt, "+xml"):
		return true
	}
	switch mt {
	case "application/json", "application/javascript", "application/xml", "application/x-ndjson":
		return true
	}
	return false
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(name), "gzip") {
			return !strings.Contains(strings.ReplaceAll(params, " ", ""), "q=0")
		}
	}
	return false
}

type gzipWriter struct {
	http.ResponseWriter
	min      int
	status   int
	buf      []byte
	decided  bool
	compress bool
	gz       *gzip.Writer
}

func (w *gzipWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}

func (w *gzipWriter) Write(b []byte) (int, error) {
	if w.decided {
		return w.out(b)
	}
	w.buf = append(w.buf, b...)
	if len(w.buf) >= w.min {
		if err := w.decide(true); err != nil {
			return 0, err
		}
	}
	return len(b), nil
}

// decide fixes the response headers, sends the status line and drains the buffer.
func (w *gzipWriter) decide(allowCompress bool) error {
	w.decided = true
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	h := w.Header()
	if allowCompress && bodyAllowed(status) && status != http.StatusPartialContent && h.Get("Content-Encoding") == "" {
		if h.Get("Content-Type") == "" {
			h.Set("Content-Type", http.DetectContentType(w.buf))
		}
		w.compress = compressible(h.Get("Content-Type"))
	}
	if w.compress {
		h.Set("Content-Encoding", "gzip")
		h.Del("Content-Length")
		w.gz = gzipPool.Get().(*gzip.Writer)
		w.gz.Reset(w.ResponseWriter)
	}
	w.ResponseWriter.WriteHeader(status)
	if len(w.buf) > 0 {
		_, err := w.out(w.buf)
		w.buf = nil
		return err
	}
	return nil
}

func (w *gzipWriter) out(b []byte) (int, error) {
	if w.compress {
		return w.gz.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// Flush implements http.Flusher; a streaming response is never held back.
func (w *gzipWriter) Flush() {
	if !w.decided {
		_ = w.decide(true)
	}
	if w.compress {
		_ = w.gz.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer (deadlines).
func (w *gzipWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *gzipWriter) finish() error {
	if !w.decided {
		if w.status == 0 && len(w.buf) == 0 {
			return nil // the handler wrote nothing: net/http sends its default
		}
		if err := w.decide(false); err != nil { // too small to be worth compressing
			return err
		}
	}
	if w.gz != nil {
		err := w.gz.Close()
		gzipPool.Put(w.gz)
		w.gz = nil
		return err
	}
	return nil
}

// Gzip compresses text and JSON responses for clients that send
// "Accept-Encoding: gzip". Responses smaller than minSize bytes (default
// 1024), streams (text/event-stream), WebSocket upgrades, partial content
// and already-encoded responses are left alone.
func Gzip(minSize ...int) Middleware {
	min := 1024
	if len(minSize) > 0 && minSize[0] > 0 {
		min = minSize[0]
	}
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) error {
			if !acceptsGzip(c.R) || c.R.Header.Get("Upgrade") != "" || c.R.Method == http.MethodHead {
				return next(c)
			}
			c.W.Header().Add("Vary", "Accept-Encoding")
			gw := &gzipWriter{ResponseWriter: c.W, min: min}
			orig := c.W
			c.W = gw
			err := next(c)
			if err != nil {
				// Let the error responder write plain, uncompressed JSON.
				c.W = orig
				if !gw.decided {
					return err
				}
				_ = gw.finish()
				return err
			}
			c.W = orig
			return gw.finish()
		}
	}
}
