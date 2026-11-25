package ws_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/naufalraism/aemeath"
	"github.com/naufalraism/aemeath/ws"
)

func echoServer(t *testing.T) *httptest.Server {
	t.Helper()
	app := aemeath.New()
	app.Use(aemeath.Logger()) // the wrapped ResponseWriter must still be hijackable
	app.Handle("GET", "/ws", func(c *aemeath.Ctx) error {
		conn, err := ws.Upgrade(c)
		if err != nil {
			return nil
		}
		defer func() { _ = conn.CloseNow() }()
		for {
			mt, msg, err := conn.Read(c.Context())
			if err != nil {
				return nil
			}
			if err := conn.Write(c.Context(), mt, msg); err != nil {
				return nil
			}
		}
	})
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv
}

func wsURL(srv *httptest.Server) string { return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws" }

// dial connects as a client; origin, when set, is sent as the Origin header.
func dial(t *testing.T, url, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	opts := &websocket.DialOptions{}
	if origin != "" {
		opts.HTTPHeader = http.Header{"Origin": {origin}}
	}
	conn, resp, err := websocket.Dial(ctx, url, opts)
	if conn != nil {
		conn.SetReadLimit(-1)
		t.Cleanup(func() { _ = conn.CloseNow() })
	}
	return conn, resp, err
}

func TestEchoThroughTheMiddlewareChain(t *testing.T) {
	srv := echoServer(t)
	conn, _, err := dial(t, wsURL(srv), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(t.Context(), websocket.MessageText, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	_, msg, err := conn.Read(t.Context())
	if err != nil || string(msg) != "ping" {
		t.Fatalf("echo = %q, err = %v", msg, err)
	}
}

func TestCrossOriginIsRejectedByDefault(t *testing.T) {
	srv := echoServer(t)
	_, resp, err := dial(t, wsURL(srv), "https://evil.example")
	if err == nil {
		t.Fatal("a cross-origin upgrade must be refused")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("response = %v, want 403", resp)
	}
}

func TestSameOriginIsAccepted(t *testing.T) {
	srv := echoServer(t)
	if _, _, err := dial(t, wsURL(srv), srv.URL); err != nil {
		t.Fatalf("same-origin upgrade failed: %v", err)
	}
}

// onlyUnwrap is a middleware wrapper like the ones in session, metrics and
// tracing: it implements Unwrap but not Hijack.
type onlyUnwrap struct{ http.ResponseWriter }

func (w onlyUnwrap) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// A wrapper without Hijack anywhere in the chain used to make Upgrade fail
// with 500 ("response does not implement http.Hijacker").
func TestUpgradeThroughWrappersWithoutHijack(t *testing.T) {
	wrap := func(next aemeath.HandlerFunc) aemeath.HandlerFunc {
		return func(c *aemeath.Ctx) error {
			c.W = onlyUnwrap{c.W}
			return next(c)
		}
	}
	for name, mws := range map[string][]aemeath.Middleware{
		"wrapper only":          {wrap},
		"Logger outside":        {aemeath.Logger(), wrap},
		"Logger inside":         {wrap, aemeath.Logger()},
		"two wrappers + Logger": {wrap, aemeath.Logger(), wrap},
	} {
		t.Run(name, func(t *testing.T) {
			app := aemeath.New()
			app.Use(mws...)
			app.Handle("GET", "/ws", func(c *aemeath.Ctx) error {
				conn, err := ws.Upgrade(c)
				if err != nil {
					return nil
				}
				defer func() { _ = conn.CloseNow() }()
				return conn.Write(c.Context(), websocket.MessageText, []byte("hi"))
			})
			srv := httptest.NewServer(app)
			defer srv.Close()
			conn, resp, err := dial(t, wsURL(srv), "")
			if err != nil {
				code := 0
				if resp != nil {
					code = resp.StatusCode
				}
				t.Fatalf("dial failed (status %d): %v", code, err)
			}
			if _, msg, err := conn.Read(t.Context()); err != nil || string(msg) != "hi" {
				t.Fatalf("got %q, %v", msg, err)
			}
		})
	}
}

// Read buffers a whole message: without a limit one client could make the
// server hold any amount in memory.
func TestDefaultReadLimit(t *testing.T) {
	got := make(chan error, 1)
	app := aemeath.New()
	app.Handle("GET", "/ws", func(c *aemeath.Ctx) error {
		conn, err := ws.Upgrade(c)
		if err != nil {
			return nil
		}
		defer func() { _ = conn.CloseNow() }()
		_, _, err = conn.Read(c.Context())
		got <- err
		return nil
	})
	srv := httptest.NewServer(app)
	defer srv.Close()

	for _, tc := range []struct {
		size    int
		wantErr bool
	}{{1000, false}, {int(ws.DefaultReadLimit), false}, {int(ws.DefaultReadLimit) + 1, true}, {8 << 20, true}} {
		conn, _, err := dial(t, wsURL(srv), "")
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Write(t.Context(), websocket.MessageBinary, make([]byte, tc.size))
		err = <-got
		_ = conn.CloseNow()
		if (err != nil) != tc.wantErr {
			t.Errorf("message of %d bytes: read error %v, want error=%v", tc.size, err, tc.wantErr)
		}
		if tc.wantErr && !errors.Is(err, websocket.ErrMessageTooBig) {
			t.Errorf("message of %d bytes: %v, want ErrMessageTooBig", tc.size, err)
		}
	}
}

// Compression stays off even when the client offers it: per-message deflate
// keeps a window per connection (memory), and a compressed message that holds
// a secret next to input from another party leaks through its size.
func TestCompressionIsNotNegotiated(t *testing.T) {
	srv := echoServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, wsURL(srv), &websocket.DialOptions{CompressionMode: websocket.CompressionContextTakeover})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	if ext := resp.Header.Get("Sec-WebSocket-Extensions"); ext != "" {
		t.Errorf("the server agreed to %q", ext)
	}
}
