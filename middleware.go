package aemeath

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
)

type statusWriter struct {
	http.ResponseWriter
	status  int
	written bool
}

func (w *statusWriter) WriteHeader(code int) {
	if w.written {
		return
	}
	w.written = true
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Flush implements http.Flusher so streaming responses (e.g. SSE) keep working.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		if !w.written {
			w.WriteHeader(http.StatusOK)
		}
		f.Flush()
	}
}

// Hijack implements http.Hijacker so WebSocket upgrades can take over the connection.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	// ResponseController follows Unwrap, so a wrapper below this one that
	// does not implement Hijack itself does not break WebSocket upgrades.
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

// Logger logs every request as one record of the app's logger (see
// App.SetLogger): method, path, status, duration_ms, remote and request_id.
// 5xx are logged at Error level, 4xx at Warn, everything else at Info.
func Logger() Middleware {
	return accessLog(func(c *Ctx) *slog.Logger { return c.app.Logger() })
}

// Recover turns panics into a 500 response and logs the stack trace.
func Recover() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) (err error) {
			defer func() {
				if r := recover(); r != nil {
					c.Logger().Error("aemeath: panic", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
					err = NewError(http.StatusInternalServerError, "internal server error")
				}
			}()
			return next(c)
		}
	}
}
