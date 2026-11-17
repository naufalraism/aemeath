//go:build !windows

// Package hostile puts the framework in bad conditions over real TCP:
// clients that disconnect mid-request, huge bodies and headers, slow
// clients (slowloris), a shutdown with requests in flight, cancelled
// contexts. Each test logs what happened and asserts the safe outcome.
package hostile

import (
	"bufio"
	"bytes"
	"io"
	"log"
	"net"
	"net/http"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
)

// server runs app on 127.0.0.1 with the framework's own server settings
// (app.NewServer), optionally adjusted, and returns its address.
func server(t *testing.T, app *aemeath.App, adjust func(*http.Server)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := app.NewServer(ln.Addr().String())
	if adjust != nil {
		adjust(srv)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

// logs captures the standard logger (Logger, internal errors) for a test.
type logs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logs) Write(p []byte) (int, error) { l.mu.Lock(); defer l.mu.Unlock(); return l.buf.Write(p) }
func (l *logs) String() string              { l.mu.Lock(); defer l.mu.Unlock(); return l.buf.String() }

func captureLogs(t *testing.T) *logs {
	l := &logs{}
	prev := log.Writer()
	log.SetOutput(l)
	t.Cleanup(func() { log.SetOutput(prev) })
	return l
}

func dial(t *testing.T, addr string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// readResponse reads one response from c; status is 0 if the server closed
// the connection without one.
func readResponse(c net.Conn, d time.Duration) (status int, body string, err error) {
	_ = c.SetReadDeadline(time.Now().Add(d))
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), nil
}

func settle(limit int, d time.Duration) int {
	deadline := time.Now().Add(d)
	for {
		n := runtime.NumGoroutine()
		if n <= limit || time.Now().After(deadline) {
			return n
		}
		time.Sleep(20 * time.Millisecond)
	}
}
