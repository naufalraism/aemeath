package aemeath

import (
	"errors"
	"log"
	"net/http"
	"runtime/debug"
	"time"
)

// statusWriter membungkus http.ResponseWriter untuk melacak status code
// dan mencegah WriteHeader ganda (yang akan panic di stdlib).
type statusWriter struct {
	http.ResponseWriter
	status  int
	written bool
}

func (w *statusWriter) WriteHeader(code int) {
	if w.written {
		// Sudah ditulis sebelumnya — abaikan, jangan panic.
		return
	}
	w.written = true
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Write memastikan header terkirim dengan status default 200 kalau
// handler menulis body tanpa eksplisit WriteHeader.
func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap memungkinkan http.ResponseController (Go 1.20+) mengakses
// fitur underlying writer seperti Flush/Hijack (WebSocket/SSE).
func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Logger mencatat method, path, status, dan durasi.
// Panic juga dicatat (sebagai 500) sebelum di-repanic agar ditangani Recover().
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
					panic(r) // teruskan ke Recover() di lapisan luar
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
