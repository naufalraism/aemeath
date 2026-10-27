package aemeath

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"runtime/debug"
	"time"
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
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("aemeath: underlying ResponseWriter does not implement http.Hijacker")
}

// Logger logs the method, path, status, and duration of every request.
func Logger() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) (err error) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: c.W, status: http.StatusOK}
			c.W = sw
			defer func() {
				// EscapedPath keeps control characters (e.g. %0A) from forging log lines.
				path := c.R.URL.EscapedPath()
				if r := recover(); r != nil {
					log.Printf("%s %s %d %v panic: %v",
						c.R.Method, path, http.StatusInternalServerError,
						time.Since(start), r)
					panic(r)
				}
				status := sw.status
				if err != nil {
					var he *HTTPError
					if errors.As(err, &he) {
						status = he.Code
					} else {
						status = http.StatusInternalServerError
					}
				}
				if id := c.RequestID(); id != "" {
					log.Printf("%s %s %d %v id=%s", c.R.Method, path, status, time.Since(start), id)
					return
				}
				log.Printf("%s %s %d %v", c.R.Method, path, status, time.Since(start))
			}()
			err = next(c)
			return err
		}
	}
}

// Recover turns panics into a 500 response and logs the stack trace.
func Recover() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) (err error) {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("aemeath: panic: %v\n%s", r, debug.Stack())
					err = NewError(http.StatusInternalServerError, "internal server error")
				}
			}()
			return next(c)
		}
	}
}
