package ws_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

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
		defer func() { _ = conn.Close() }()
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return nil
			}
			if err := conn.WriteMessage(mt, msg); err != nil {
				return nil
			}
		}
	})
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv
}

func wsURL(srv *httptest.Server) string { return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws" }

func TestEchoThroughTheMiddlewareChain(t *testing.T) {
	srv := echoServer(t)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL(srv), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil || string(msg) != "ping" {
		t.Fatalf("echo = %q, err = %v", msg, err)
	}
}

func TestCrossOriginIsRejectedByDefault(t *testing.T) {
	srv := echoServer(t)
	_, resp, err := websocket.DefaultDialer.Dial(wsURL(srv), http.Header{"Origin": {"https://evil.example"}})
	if err == nil {
		t.Fatal("a cross-origin upgrade must be refused")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("response = %v, want 403", resp)
	}
}

func TestSameOriginIsAccepted(t *testing.T) {
	srv := echoServer(t)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL(srv), http.Header{"Origin": {srv.URL}})
	if err != nil {
		t.Fatalf("same-origin upgrade failed: %v", err)
	}
	_ = conn.Close()
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
				defer func() { _ = conn.Close() }()
				return conn.WriteMessage(websocket.TextMessage, []byte("hi"))
			})
			srv := httptest.NewServer(app)
			defer srv.Close()
			conn, resp, err := websocket.DefaultDialer.Dial(wsURL(srv), nil)
			if err != nil {
				code := 0
				if resp != nil {
					code = resp.StatusCode
				}
				t.Fatalf("dial failed (status %d): %v", code, err)
			}
			defer func() { _ = conn.Close() }()
			if _, msg, err := conn.ReadMessage(); err != nil || string(msg) != "hi" {
				t.Fatalf("got %q, %v", msg, err)
			}
		})
	}
}

// ReadMessage buffers a whole message: without a limit one client could make
// the server hold 64 MiB (or more) in memory.
func TestDefaultReadLimit(t *testing.T) {
	got := make(chan error, 1)
	app := aemeath.New()
	app.Handle("GET", "/ws", func(c *aemeath.Ctx) error {
		conn, err := ws.Upgrade(c)
		if err != nil {
			return nil
		}
		defer func() { _ = conn.Close() }()
		_, _, err = conn.ReadMessage()
		got <- err
		return nil
	})
	srv := httptest.NewServer(app)
	defer srv.Close()

	for _, tc := range []struct {
		size    int
		wantErr bool
	}{{1000, false}, {int(ws.DefaultReadLimit), false}, {int(ws.DefaultReadLimit) + 1, true}, {8 << 20, true}} {
		conn, _, err := websocket.DefaultDialer.Dial(wsURL(srv), nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.WriteMessage(websocket.BinaryMessage, make([]byte, tc.size))
		err = <-got
		_ = conn.Close()
		if (err != nil) != tc.wantErr {
			t.Errorf("message of %d bytes: read error %v, want error=%v", tc.size, err, tc.wantErr)
		}
		if tc.wantErr && !errors.Is(err, websocket.ErrReadLimit) {
			t.Errorf("message of %d bytes: %v, want ErrReadLimit", tc.size, err)
		}
	}
}
