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

func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// PERBAIKAN: Tambahkan Hijack() agar WebSocket bisa mengambil alih koneksi
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("aemeath: underlying ResponseWriter does not implement http.Hijacker")
}

// Logger mencatat method, path, status, dan durasi.
func Logger() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) (err error) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: c.W, status: http.StatusOK}
			c.W = sw
			defer func() {
				if r := recover(); r != nil {
					log.Printf("%s %s %d %v panic: %v",
						c.R.Method, c.R.URL.Path, http.StatusInternalServerError,
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
				log.Printf("%s %s %d %v", c.R.Method, c.R.URL.Path, status, time.Since(start))
			}()
			err = next(c)
			return err
		}
	}
}

// Recover mengubah panic menjadi respons 500 dan mencatat stack trace.
func Recover() Middleware {
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Ctx) (err error) {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("aemeath: panic: %v\n%s", r, debug.Stack())
					err = NewError(http.StatusInternalServerError, "terjadi kesalahan internal")
				}
			}()
			return next(c)
		}
	}
}
