//go:build !windows

package hostile

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/naufalraism/aemeath"
)

// Slowloris with the framework's real server settings (app.NewServer):
// headers sent a byte at a time, a body sent a byte at a time, a client that
// does not read its response, and many such clients at once.
func TestSlowClients(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the real timeouts (up to 61 s)")
	}
	app := aemeath.New()
	aemeath.POST(app, "/json", func(_ *aemeath.Ctx, in item) (item, error) { return in, nil })
	app.Handle("GET", "/big", func(c *aemeath.Ctx) error {
		_, err := io.WriteString(c.W, strings.Repeat("x", 64<<20))
		return err
	})
	// The server's own view: when it closes each connection (ConnState).
	var closedAt sync.Map // client address -> time
	addr := server(t, app, func(srv *http.Server) {
		srv.ConnState = func(c net.Conn, st http.ConnState) {
			if st == http.StateClosed {
				closedAt.Store(c.RemoteAddr().String(), time.Now())
			}
		}
	})
	t.Logf("server settings: ReadHeaderTimeout 5s, ReadTimeout 15s, WriteTimeout 15s, IdleTimeout 60s, MaxHeaderBytes 1 MB (net/http default)")
	serverClosed := func(c net.Conn, start time.Time, max time.Duration) (time.Duration, bool) {
		deadline := time.Now().Add(max)
		for time.Now().Before(deadline) {
			if v, ok := closedAt.Load(c.LocalAddr().String()); ok {
				return v.(time.Time).Sub(start), true
			}
			time.Sleep(50 * time.Millisecond)
		}
		return time.Since(start), false
	}

	var wg sync.WaitGroup
	report := func(name string, f func() (time.Duration, bool)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, closed := f()
			t.Logf("%-46s closed by the server: %v, after %v", name, closed, d.Round(100*time.Millisecond))
			if !closed {
				t.Errorf("%s: the server kept the connection", name)
			}
		}()
	}
	report("headers, one byte every 500 ms", func() (time.Duration, bool) {
		c := dial(t, addr)
		start := time.Now()
		go func() {
			for _, b := range []byte("GET /big HTTP/1.1\r\nHost: x\r\nX-Slow: " + strings.Repeat("z", 100)) {
				if _, err := c.Write([]byte{b}); err != nil {
					return
				}
				time.Sleep(500 * time.Millisecond)
			}
		}()
		return serverClosed(c, start, 30*time.Second)
	})
	report("body, one byte every 500 ms", func() (time.Duration, bool) {
		c := dial(t, addr)
		start := time.Now()
		_, _ = fmt.Fprintf(c, "POST /json HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nContent-Length: 1000\r\n\r\n")
		go func() {
			for i := 0; i < 1000; i++ {
				if _, err := c.Write([]byte("a")); err != nil {
					return
				}
				time.Sleep(500 * time.Millisecond)
			}
		}()
		return serverClosed(c, start, 30*time.Second)
	})
	report("64 MB response, client never reads", func() (time.Duration, bool) {
		c := dial(t, addr)
		if tc, ok := c.(*net.TCPConn); ok {
			_ = tc.SetReadBuffer(4096)
		}
		start := time.Now()
		_, _ = fmt.Fprintf(c, "GET /big HTTP/1.1\r\nHost: x\r\n\r\n")
		return serverClosed(c, start, 30*time.Second)
	})
	report("idle keep-alive after one request", func() (time.Duration, bool) {
		c := dial(t, addr)
		_, _ = fmt.Fprintf(c, "POST /json HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n{}")
		_, _, _ = readResponse(c, 5*time.Second)
		start := time.Now()
		return serverClosed(c, start, 70*time.Second)
	})
	wg.Wait()

	// 1,000 slow clients at once: each holds a goroutine until its timeout.
	before := runtime.NumGoroutine()
	conns := make([]net.Conn, 0, 1000)
	for i := 0; i < 1000; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Logf("dial %d: %v", i, err)
			break
		}
		_, _ = io.WriteString(c, "GET / HTTP/1.1\r\n")
		conns = append(conns, c)
	}
	time.Sleep(500 * time.Millisecond)
	peak := runtime.NumGoroutine()
	left := settle(before+5, 10*time.Second)
	t.Logf("%d slow clients at once: goroutines %d -> %d while they wait -> %d after ReadHeaderTimeout (no limit on connections)", len(conns), before, peak, left)
	for _, c := range conns {
		_ = c.Close()
	}
}

// An SSE client that stays connected but stops reading.
func TestStreamToAClientThatStopsReading(t *testing.T) {
	if testing.Short() {
		t.Skip("waits")
	}
	var sent atomic.Int64
	ended := make(chan error, 1)
	app := aemeath.New()
	app.Handle("GET", "/events", func(c *aemeath.Ctx) error {
		s, err := c.SSE()
		if err != nil {
			return err
		}
		big := strings.Repeat("y", 64<<10)
		for {
			if err := s.Send("chunk", big); err != nil {
				ended <- err
				return nil
			}
			sent.Add(1)
			select {
			case <-c.Context().Done():
				ended <- c.Context().Err()
				return nil
			default:
			}
		}
	})
	addr := server(t, app, nil)
	c := dial(t, addr)
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetReadBuffer(4096)
	}
	_, _ = fmt.Fprintf(c, "GET /events HTTP/1.1\r\nHost: x\r\n\r\n")
	start := time.Now()
	select {
	case err := <-ended:
		t.Logf("the stream handler returned after %v: %v", time.Since(start).Round(100*time.Millisecond), err)
	case <-time.After(20 * time.Second):
		t.Errorf("after 20 s the stream handler is still blocked in Send (%d events written, ~%d KB), with no deadline: a client that stops reading holds it for as long as it keeps the connection open", sent.Load(), sent.Load()*64)
	}
}

// SIGTERM while requests run: a short one, one longer than the 10 s grace
// period, a stream, and a new request that arrives during shutdown.
func TestShutdownWithRequestsInFlight(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the 10 s grace period")
	}
	l := captureLogs(t)
	shortDone := make(chan struct{})
	var longCtxErr atomic.Value
	app := aemeath.New()
	app.EnableHealth()
	app.Handle("GET", "/short", func(c *aemeath.Ctx) error {
		time.Sleep(2 * time.Second)
		close(shortDone)
		return c.Text(200, "finished")
	})
	app.Handle("GET", "/long", func(c *aemeath.Ctx) error {
		select {
		case <-time.After(30 * time.Second):
			return c.Text(200, "finished")
		case <-c.Context().Done():
			longCtxErr.Store(fmt.Sprint(c.Context().Err()))
			return c.Context().Err()
		}
	})
	app.Handle("GET", "/events", func(c *aemeath.Ctx) error {
		s, err := c.SSE()
		if err != nil {
			return err
		}
		for {
			if err := s.Comment("ping"); err != nil {
				return nil
			}
			select {
			case <-c.Context().Done():
				return nil
			case <-time.After(100 * time.Millisecond):
			}
		}
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	keep := make(chan os.Signal, 1)
	signal.Notify(keep, syscall.SIGTERM)
	defer signal.Stop(keep)
	served := make(chan error, 1)
	go func() { served <- app.Serve(app.NewServer(addr)) }()
	for i := 0; i < 200; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	type result struct {
		code int
		body string
		err  error
		at   time.Time
	}
	run := func(path string) chan result {
		ch := make(chan result, 1)
		go func() {
			c, err := net.Dial("tcp", addr)
			if err != nil {
				ch <- result{err: err, at: time.Now()}
				return
			}
			defer func() { _ = c.Close() }()
			_, _ = fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: x\r\n\r\n", path)
			if path == "/events" {
				_, _ = closedWithin(c, 30*time.Second)
				ch <- result{code: 200, body: "(stream ended)", at: time.Now()}
				return
			}
			code, body, err := readResponse(c, 30*time.Second)
			ch <- result{code, strings.TrimSpace(body), err, time.Now()}
		}()
		return ch
	}
	short, long, events := run("/short"), run("/long"), run("/events")
	time.Sleep(300 * time.Millisecond)
	start := time.Now()
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	late := run("/short") // arrives while shutting down
	var stopped time.Duration
	select {
	case err := <-served:
		stopped = time.Since(start)
		t.Logf("Serve returned %v after SIGTERM: %v", stopped.Round(100*time.Millisecond), err)
	case <-time.After(30 * time.Second):
		t.Fatal("Serve did not return within 30 s of SIGTERM")
	}
	for name, ch := range map[string]chan result{"/short (2 s)": short, "/long (30 s)": long, "/events (stream)": events, "new request during shutdown": late} {
		r := <-ch
		t.Logf("%-28s -> %d %q err=%v, %v after SIGTERM", name, r.code, r.body, r.err, r.at.Sub(start).Round(100*time.Millisecond))
		if name == "/short (2 s)" && r.code != http.StatusOK {
			t.Error("a request that finishes within the grace period must complete")
		}
	}
	select {
	case <-shortDone:
	default:
		t.Error("the short handler did not run to the end")
	}
	t.Logf("long handler's context: %v", longCtxErr.Load())
	t.Logf("log:\n%s", strings.TrimSpace(l.String()))
}

// closedWithin reports how long the server took to close c (reading until
// EOF or error), up to max.
func closedWithin(c net.Conn, max time.Duration) (time.Duration, bool) {
	start := time.Now()
	_ = c.SetReadDeadline(time.Now().Add(max))
	buf := make([]byte, 4096)
	for {
		if _, err := c.Read(buf); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return time.Since(start), false
			}
			return time.Since(start), true
		}
	}
}

// The write timeout is per event: a stream that is quiet for longer than it
// (and a client that keeps reading) is not cut.
func TestStreamQuietLongerThanTheWriteTimeout(t *testing.T) {
	app := aemeath.New()
	app.Handle("GET", "/events", func(c *aemeath.Ctx) error {
		s, err := c.SSE()
		if err != nil {
			return err
		}
		s.SetWriteTimeout(300 * time.Millisecond)
		for i := 0; i < 3; i++ {
			if err := s.Send("tick", i); err != nil {
				return err
			}
			time.Sleep(700 * time.Millisecond) // quiet for more than twice the timeout
		}
		return nil
	})
	addr := server(t, app, nil)
	c := dial(t, addr)
	_, _ = fmt.Fprintf(c, "GET /events HTTP/1.1\r\nHost: x\r\n\r\n")
	_, body, err := readResponse(c, 5*time.Second)
	t.Logf("received %d events, err=%v", strings.Count(body, "event: tick"), err)
	if n := strings.Count(body, "event: tick"); n != 3 {
		t.Errorf("received %d of 3 events: the per-event timeout cut a stream that was only quiet", n)
	}
}
