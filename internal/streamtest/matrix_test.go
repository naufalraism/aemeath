package streamtest

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/csrf"
	"github.com/naufalraism/aemeath/metrics"
	"github.com/naufalraism/aemeath/session"
	"github.com/naufalraism/aemeath/tracing"
	"github.com/naufalraism/aemeath/ws"
)

// setup installs one middleware stack on a fresh app.
type setup func(t *testing.T, app *aemeath.App)

func use(mws ...func() aemeath.Middleware) setup {
	return func(_ *testing.T, app *aemeath.App) {
		for _, mw := range mws {
			app.Use(mw())
		}
	}
}

var (
	logger    = func() aemeath.Middleware { return aemeath.Logger() }
	slogLog   = func() aemeath.Middleware { return aemeath.SlogLogger(nil) }
	recoverMW = func() aemeath.Middleware { return aemeath.Recover() }
	gzipMW    = func() aemeath.Middleware { return aemeath.Gzip() }
	requestID = func() aemeath.Middleware { return aemeath.RequestID() }
	secHdr    = func() aemeath.Middleware { return aemeath.SecurityHeaders() }
	corsMW    = func() aemeath.Middleware { return aemeath.CORS() }
	timeout   = func() aemeath.Middleware { return aemeath.Timeout(30 * time.Second) }
	realIP    = func() aemeath.Middleware { return aemeath.RealIP(aemeath.PrivateNetworks...) }
	rateLimit = func() aemeath.Middleware { return aemeath.RateLimiter(1000, time.Minute) }
	sessionMW = func() aemeath.Middleware {
		return session.Middleware(session.Config{Store: session.NewMemoryStore(), Insecure: true})
	}
	csrfMW    = func() aemeath.Middleware { return csrf.Middleware(csrf.Config{}) }
	metricsMW = func() aemeath.Middleware { return metrics.New().Middleware() }
	tracingMW = func() aemeath.Middleware {
		return tracing.New(tracing.Config{ServiceName: "streamtest"}).Middleware()
	}
)

func fileLogger(t *testing.T, app *aemeath.App) {
	p, err := aemeath.NewFileLogger(filepath.Join(t.TempDir(), "app.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	if err := app.RegisterPlugin(p); err != nil {
		t.Fatal(err)
	}
}

func stacks() map[string]setup {
	all := []func() aemeath.Middleware{realIP, requestID, logger, slogLog, recoverMW, secHdr, corsMW, rateLimit, timeout, gzipMW, metricsMW, tracingMW, sessionMW, csrfMW}
	reversed := make([]func() aemeath.Middleware, len(all)-1)
	for i, mw := range all[:len(all)-1] { // csrf must stay inside session
		reversed[len(reversed)-1-i] = mw
	}
	return map[string]setup{
		"none":            use(),
		"Logger":          use(logger),
		"SlogLogger":      use(slogLog),
		"Recover":         use(recoverMW),
		"Gzip":            use(gzipMW),
		"RequestID":       use(requestID),
		"SecurityHeaders": use(secHdr),
		"CORS":            use(corsMW),
		"Timeout":         use(timeout),
		"RealIP":          use(realIP),
		"RateLimiter":     use(rateLimit),
		"session":         use(sessionMW),
		"session+csrf":    use(sessionMW, csrfMW),
		"metrics":         use(metricsMW),
		"tracing":         use(tracingMW),
		"FileLogger":      fileLogger,
		"all":             use(all...),
		"all reversed":    use(reversed...),
		"all + FileLogger": func(t *testing.T, app *aemeath.App) {
			fileLogger(t, app)
			use(all...)(t, app)
		},
	}
}

func serve(t *testing.T, install setup, routes func(app *aemeath.App)) *httptest.Server {
	t.Helper()
	app := aemeath.New()
	install(t, app)
	routes(app)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv
}

// An SSE event must reach the client when it is sent, not when the handler
// returns: a wrapper that buffers or drops Flush would hold it back.
func TestSSEArrivesEventByEvent(t *testing.T) {
	for name, install := range stacks() {
		t.Run(name, func(t *testing.T) {
			release := make(chan struct{})
			srv := serve(t, install, func(app *aemeath.App) {
				app.Handle("GET", "/events", func(c *aemeath.Ctx) error {
					s, err := c.SSE()
					if err != nil {
						return err
					}
					if err := s.Send("first", map[string]int{"n": 1}); err != nil {
						return err
					}
					select {
					case <-release:
					case <-c.Context().Done():
						return nil
					}
					return s.Send("second", map[string]int{"n": 2})
				})
			})
			defer close(release)

			req, _ := http.NewRequest("GET", srv.URL+"/events", nil)
			req.Header.Set("Accept", "text/event-stream")
			req.Header.Set("Accept-Encoding", "gzip") // what a browser sends; the stream must not be held back by compression
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
				t.Fatalf("status %d, Content-Type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
			}
			if enc := resp.Header.Get("Content-Encoding"); enc != "" {
				t.Fatalf("the event stream is compressed (%s): events are buffered until the compressor flushes", enc)
			}
			got := make(chan string, 1)
			go func() {
				r := bufio.NewReader(resp.Body)
				var b strings.Builder
				for {
					line, err := r.ReadString('\n')
					b.WriteString(line)
					if err != nil || line == "\n" {
						got <- b.String()
						return
					}
				}
			}()
			select {
			case ev := <-got:
				if !strings.Contains(ev, "event: first") || !strings.Contains(ev, `{"n":1}`) {
					t.Fatalf("first event: %q", ev)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("the first event did not arrive while the handler was still running (Flush is not passed on)")
			}
		})
	}
}

// A file sent with c.File must arrive whole, with its length, and range
// requests must work.
func TestFileDownload(t *testing.T) {
	content := bytes.Repeat([]byte("0123456789abcdef"), 40_000) // 640 KiB
	path := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, install := range stacks() {
		t.Run(name, func(t *testing.T) {
			srv := serve(t, install, func(app *aemeath.App) {
				app.Handle("GET", "/file", func(c *aemeath.Ctx) error { return c.File(path) })
			})
			get := func(h map[string]string) (*http.Response, []byte) {
				req, _ := http.NewRequest("GET", srv.URL+"/file", nil)
				for k, v := range h {
					req.Header.Set(k, v)
				}
				resp, err := srv.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = resp.Body.Close() }()
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("reading the body: %v", err)
				}
				return resp, body
			}
			resp, body := get(nil) // Go's client asks for gzip and decompresses
			if resp.StatusCode != 200 || !bytes.Equal(body, content) {
				t.Fatalf("status %d, %d bytes (want %d)", resp.StatusCode, len(body), len(content))
			}
			resp, body = get(map[string]string{"Range": "bytes=10-19", "Accept-Encoding": "identity"})
			if resp.StatusCode != 206 || string(body) != "abcdef0123" {
				t.Fatalf("range request: status %d, body %q", resp.StatusCode, body)
			}
		})
	}
}

// A WebSocket must upgrade and echo through every stack.
func TestWebSocketEcho(t *testing.T) {
	for name, install := range stacks() {
		t.Run(name, func(t *testing.T) {
			srv := serve(t, install, func(app *aemeath.App) {
				app.Handle("GET", "/ws", func(c *aemeath.Ctx) error {
					conn, err := ws.Upgrade(c)
					if err != nil {
						return nil
					}
					defer func() { _ = conn.Close() }()
					for {
						kind, msg, err := conn.ReadMessage()
						if err != nil {
							return nil
						}
						if err := conn.WriteMessage(kind, msg); err != nil {
							return nil
						}
					}
				})
			})
			conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", nil)
			if err != nil {
				code := 0
				if resp != nil {
					code = resp.StatusCode
				}
				t.Fatalf("upgrade failed (status %d): %v", code, err)
			}
			defer func() { _ = conn.Close() }()
			for _, msg := range []string{"hello", strings.Repeat("x", 100_000)} {
				if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
					t.Fatal(err)
				}
				_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				if _, got, err := conn.ReadMessage(); err != nil || string(got) != msg {
					t.Fatalf("echo of %d bytes: %d bytes, %v", len(msg), len(got), err)
				}
			}
		})
	}
}
